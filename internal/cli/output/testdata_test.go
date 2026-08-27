package output

// fixtureResult is a minimal Result used across this package's tests. Its
// JSON tags matter: tests assert on the exact marshaled shape.
type fixtureResult struct {
	Name string `json:"name"`
}

func (f fixtureResult) Human() string { return "name: " + f.Name }
