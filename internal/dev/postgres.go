package dev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultPostgresImage is the image `nise dev` runs, pinned to an exact
// tag and fully qualified with its registry.
//
// The tag is never "latest". A floating tag means two developers on the
// same commit can be running different major versions of PostgreSQL, and
// the one who is wrong finds out through a syntax error in a migration
// months later. The registry is spelled out because Podman, unlike Docker,
// has no implicit docker.io prefix and will otherwise prompt for a registry
// or fail outright in a non-interactive run.
const DefaultPostgresImage = "docker.io/library/postgres:17.6-alpine"

// containerDataDir is where the postgres image stores its cluster. The
// named volume is mounted here so a restart of the container keeps the
// data.
const containerDataDir = "/var/lib/postgresql/data"

// redactedPassword is what appears wherever a connection string is printed.
// It matches internal/cli/clierr's placeholder so CLI output is consistent
// about what a removed secret looks like.
const redactedPassword = "[REDACTED]"

// Postgres is the development database container: its identity, its image,
// and where it is published. Every name is derived from the project, so two
// projects never collide and the same project always gets the same
// container and the same volume.
type Postgres struct {
	// Project is the sanitized project identifier every name derives from.
	Project string
	// Image is the pinned image reference.
	Image string
	// HostAddr is the loopback address the container port is published on.
	// It is never 0.0.0.0: the development database has a fixed, derived
	// password, so it must not be reachable from outside the machine.
	HostAddr string
	// HostPort is the published TCP port.
	HostPort string
}

// NewPostgres builds a Postgres for a project. An empty image selects
// DefaultPostgresImage; an empty address selects loopback.
func NewPostgres(project, image, hostAddr, hostPort string) Postgres {
	p := Postgres{
		Project:  slug(project),
		Image:    image,
		HostAddr: hostAddr,
		HostPort: hostPort,
	}
	if p.Image == "" {
		p.Image = DefaultPostgresImage
	}
	if p.HostAddr == "" {
		p.HostAddr = "127.0.0.1"
	}
	if p.HostPort == "" {
		p.HostPort = DefaultDatabasePort
	}
	return p
}

// ContainerName is the deterministic container name, e.g.
// "nise-dev-demoapp-postgres".
func (p Postgres) ContainerName() string { return "nise-dev-" + p.Project + "-postgres" }

// VolumeName is the deterministic data-volume name, e.g.
// "nise-dev-demoapp-pgdata". It is separate from the container so that
// removing the container (to change the image tag, say) does not destroy
// the data.
func (p Postgres) VolumeName() string { return "nise-dev-" + p.Project + "-pgdata" }

// User is the database role the container is initialized with.
func (p Postgres) User() string { return p.Project }

// Database is the database the container is initialized with.
func (p Postgres) Database() string { return p.Project }

// credential returns the development database credential.
//
// It is derived rather than random so that the same project reconnects to
// its existing volume across runs and across machines, and so that nothing
// has to be persisted anywhere to remember it. It is not a secret in any
// meaningful sense — it guards a throwaway local database published only on
// loopback — but it is still never printed: see DSN and RedactedDSN, and
// the Constraints rule that no password appears in CLI output.
func (p Postgres) credential() string { return "nisedev-" + p.Project }

// DSN is the real connection string, password included. It is passed to the
// application child through its environment and is never written to output.
func (p Postgres) DSN() string {
	return p.dsn(p.credential())
}

// RedactedDSN is the connection string as it is printed: identical to DSN
// except that the password is replaced. It is the only form that may reach
// a terminal, a log, or an error message.
func (p Postgres) RedactedDSN() string {
	return p.dsn(redactedPassword)
}

func (p Postgres) dsn(password string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		p.User(), password, p.HostAddr, p.HostPort, p.Database())
}

// createArgs is the argv that creates and starts the container. It is a
// pure function so the exact flags are testable without a container
// runtime.
//
// --restart is deliberately absent: a development database that comes back
// on every reboot without anybody asking is a surprise, and `nise dev`
// starts it in a second anyway.
func (p Postgres) createArgs() []string {
	return []string{
		"run", "--detach",
		"--name", p.ContainerName(),
		"--volume", p.VolumeName() + ":" + containerDataDir,
		"--publish", p.HostAddr + ":" + p.HostPort + ":5432",
		"--env", "POSTGRES_USER=" + p.User(),
		"--env", "POSTGRES_PASSWORD=" + p.credential(),
		"--env", "POSTGRES_DB=" + p.Database(),
		p.Image,
	}
}

// readyArgs is the argv that asks the running container whether PostgreSQL
// is accepting connections. pg_isready ships inside the image, so this
// probe is identical on Docker and on Podman and needs no client on the
// host.
//
// --host and --port are load-bearing, and their absence is a bug this
// package shipped for exactly one test run. On a cold volume the postgres
// image's entrypoint runs initdb against a *temporary* server that listens
// on the Unix socket only (it is started with listen_addresses=”), then
// shuts that server down and starts the real one. A pg_isready with no
// host connects over that socket, so it answers "accepting connections"
// during initialization — and the next thing a caller does gets
// "the database system is shutting down". Forcing the probe over TCP,
// which is also the transport the application will use, means it cannot
// pass until the real server is listening.
func (p Postgres) readyArgs() []string {
	return []string{"exec", p.ContainerName(), "pg_isready", "--quiet",
		"--host", "127.0.0.1", "--port", "5432",
		"--username", p.User(), "--dbname", p.Database()}
}

// containerState reports whether the container exists and whether it is
// running.
func (p Postgres) containerState(ctx context.Context, ex Exec, rt Runtime) (exists, running bool, err error) {
	out, err := ex.Output(ctx, rt.Command, "inspect", "--format", "{{.State.Running}}", p.ContainerName())
	if err != nil {
		// Both engines fail this call when the container does not exist.
		// That is the answer, not a fault.
		return false, false, nil
	}
	return true, strings.TrimSpace(string(out)) == "true", nil
}

// Up brings the database container to a running state, creating the volume
// and the container the first time and starting an existing stopped
// container afterwards. It returns before PostgreSQL is accepting
// connections; call WaitReady for that.
//
// progress, when non-nil, receives one line per step so a caller can show
// what is happening during a first run that has to pull an image.
func (p Postgres) Up(ctx context.Context, ex Exec, rt Runtime, progress func(string)) error {
	report := func(msg string) {
		if progress != nil {
			progress(msg)
		}
	}
	exists, running, err := p.containerState(ctx, ex, rt)
	if err != nil {
		return err
	}
	switch {
	case running:
		report("container " + p.ContainerName() + " is already running")
		return nil
	case exists:
		report("starting container " + p.ContainerName())
		if err := ex.Run(ctx, rt.Command, "start", p.ContainerName()); err != nil {
			return fmt.Errorf("starting the database container: %w", err)
		}
		return nil
	}
	report("creating volume " + p.VolumeName())
	if err := ex.Run(ctx, rt.Command, "volume", "create", p.VolumeName()); err != nil {
		return fmt.Errorf("creating the database volume: %w", err)
	}
	report("creating container " + p.ContainerName() + " from " + p.Image)
	if err := ex.Run(ctx, rt.Command, p.createArgs()...); err != nil {
		return fmt.Errorf("creating the database container: %w", err)
	}
	return nil
}

// requiredReadyStreak is how many consecutive successful probes count as
// ready. Two, not one: see readyArgs for the initialization sequence this
// guards against. The TCP-scoped probe should already be enough on its
// own; requiring the streak costs one poll interval and removes the whole
// class of "ready, then immediately shutting down" races rather than the
// one instance of it that has been observed.
const requiredReadyStreak = 2

// WaitReady polls pg_isready inside the container until PostgreSQL accepts
// connections on TCP, consistently, or timeout elapses. It is a gate, not a
// hint: nothing that needs the database is started until this returns nil,
// so an application child never has to implement its own retry loop to
// survive a cold start.
func (p Postgres) WaitReady(ctx context.Context, ex Exec, rt Runtime, timeout, interval time.Duration) error {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last error
	streak := 0
	for {
		last = ex.Run(ctx, rt.Command, p.readyArgs()...)
		if last == nil {
			streak++
			if streak >= requiredReadyStreak {
				return nil
			}
		} else {
			streak = 0
		}
		if time.Now().After(deadline) {
			if last == nil {
				last = fmt.Errorf("the probe never succeeded %d times in a row", requiredReadyStreak)
			}
			return fmt.Errorf("the database container did not become ready within %s: %w", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ErrPortMismatch reports that an existing container publishes a different
// host port than the one requested.
var ErrPortMismatch = errors.New("dev: the existing database container publishes a different port")

// PublishedPort reports the host port the running container publishes for
// PostgreSQL, using `<engine> port`, which Docker and Podman both support
// and both answer in the same "host:port" shape.
func (p Postgres) PublishedPort(ctx context.Context, ex Exec, rt Runtime) (string, error) {
	out, err := ex.Output(ctx, rt.Command, "port", p.ContainerName(), "5432/tcp")
	if err != nil {
		return "", fmt.Errorf("reading the published port of %s: %w", p.ContainerName(), err)
	}
	line := strings.TrimSpace(firstLine(string(out)))
	if line == "" {
		return "", fmt.Errorf("%s publishes no port for 5432/tcp", p.ContainerName())
	}
	if i := strings.LastIndexByte(line, ':'); i >= 0 {
		return line[i+1:], nil
	}
	return line, nil
}

// VerifyPublishedPort fails when a container left over from an earlier run
// publishes a port other than the configured one.
//
// Without this check the reused container keeps its original mapping while
// nise prints a connection string built from the flag — so the DSN in the
// startup block names a port nothing is listening on, and the application
// child is handed the same wrong address. A wrong connection string that
// looks right is worse than a refusal.
func (p Postgres) VerifyPublishedPort(ctx context.Context, ex Exec, rt Runtime) error {
	got, err := p.PublishedPort(ctx, ex, rt)
	if err != nil {
		return err
	}
	if got != p.HostPort {
		return fmt.Errorf("%w: %s publishes %s, not %s", ErrPortMismatch, p.ContainerName(), got, p.HostPort)
	}
	return nil
}

// RemoveCommand is the command that deletes the container so the next run
// recreates it with the requested mapping. The volume is not touched, so
// the data survives.
func (p Postgres) RemoveCommand(rt Runtime) string {
	return rt.Command + " rm -f " + p.ContainerName()
}

// Stop stops the container without removing it or its volume, so the next
// `nise dev` starts it again with its data intact.
func (p Postgres) Stop(ctx context.Context, ex Exec, rt Runtime) error {
	if err := ex.Run(ctx, rt.Command, "stop", p.ContainerName()); err != nil {
		return fmt.Errorf("stopping the database container: %w", err)
	}
	return nil
}

// StopCommand is the exact command a developer can copy to stop the
// container themselves. `nise dev` leaves the container running by default
// and prints this on the way out.
func (p Postgres) StopCommand(rt Runtime) string {
	return rt.Command + " stop " + p.ContainerName()
}

// slug reduces a project name to the deterministic identifier every
// container, volume, role, and database name derives from: lowercase ASCII
// letters, digits, and single hyphens, never leading or trailing, and
// bounded in length.
//
// It is deterministic and total: any input produces a usable name, and the
// same input always produces the same one, so a project's container is
// found again on the next run rather than duplicated.
func slug(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	const maxSlug = 40
	if len(out) > maxSlug {
		out = strings.Trim(out[:maxSlug], "-")
	}
	// A PostgreSQL role name cannot start with a digit without quoting,
	// and an empty name is not a name at all.
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "app-" + out
		out = strings.TrimSuffix(out, "-")
	}
	return out
}
