# Third party notices

Spine CLI statically includes the following Go modules. Their license texts are embedded in the CLI binary and printed by `spine licenses`.

| Module | Version | License file |
| --- | --- | --- |
| `github.com/evanw/esbuild` | `v0.28.2` | `esbuild-MIT.txt` |
| `golang.org/x/mod` | `v0.30.0` | `x-mod-BSD-3-Clause.txt` |
| `golang.org/x/sync` | `v0.17.0` | `x-sync-BSD-3-Clause.txt` |
| `golang.org/x/sys` | `v0.37.0` | `x-sys-BSD-3-Clause.txt` |
| `golang.org/x/tools` | `v0.38.0` | `x-tools-BSD-3-Clause.txt` |

These are the external modules in the `cmd/spine` dependency graph for this release source. Keep this list and the embedded license files aligned with `go.mod` when dependency versions or imports change.
