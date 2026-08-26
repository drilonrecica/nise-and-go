# Contributing

Nise & Go is a personal, maintainer-driven project. It is open source so others can inspect it, use it, report defects, and adapt it. It is not run as a community roadmap.

## Before opening a pull request

Open an issue first unless the change is an obvious, small correction. Do not invest substantial time before the maintainer confirms that the change fits the project.

Unsolicited pull requests may be closed without review. This is especially likely for:

- New stacks, routers, databases, or frontend alternatives.
- Large refactors.
- New abstraction or plugin systems.
- Dependency additions without a measured benefit.
- Generated changes the author cannot explain.
- Mass formatting or cleanup unrelated to a concrete defect.

There is no review or response SLA.

## Accepted changes

A useful contribution is normally:

- Small and focused.
- Consistent with the golden profile and documented decisions.
- Covered by relevant tests.
- Explicit about security and compatibility effects.
- Free of unrelated rewrites.
- Understandable and maintainable by the person submitting it.

## AI-assisted work

AI-assisted development is not automatically rejected. Low-effort generated output is.

If tools assisted the change, the submitter must still:

- Understand every material line.
- Verify behavior instead of trusting generated claims.
- Add or update meaningful tests.
- Remove invented APIs, dependencies, and unnecessary abstractions.
- Explain the design and tradeoffs without referring reviewers to an AI transcript.

Large, unexplained, or unreviewed generated submissions will be closed.

## Development expectations

Once implementation exists, the repository's documented checks are authoritative. At minimum, a change must leave formatting, static analysis, tests, deterministic generation, vulnerability checks, and relevant performance budgets passing.

Do not weaken a check merely to make a patch pass. If a check is wrong, demonstrate the problem separately.

## Security issues

Do not submit exploitable security details through a public issue or pull request. Follow [SECURITY.md](SECURITY.md).

## License

By submitting a contribution, you agree that it may be distributed under the repository's MIT License.

