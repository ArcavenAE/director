Small one from the same pass: three files in this PR are not gofmt-clean.

```
$ git checkout a4b96a3 && gofmt -l services/ scripts/
services/nvpair-engine-manager/registry.go
services/nvpair-node-info/stats_windows.go
services/shared/noderec/noderec.go
services/shared/splitlisten/splitlisten_test.go
services/tests/model_routing_interop_test.go

$ git checkout 13b6811 && gofmt -l services/ scripts/     # merge base
services/nvpair-node-info/stats_windows.go
services/shared/splitlisten/splitlisten_test.go
```

So `registry.go`, `noderec.go`, and `model_routing_interop_test.go` are the three this branch introduces; the other two are already on main and are not yours. In `registry.go` it is struct tag alignment in `Runtime`, where the new `ExtraArgs` field widens the column and the fields after it were not realigned. `gofmt -w` on the three fixes it.

Two other notes from testing this branch on Apple Silicon, so they are not mistaken for anything here:

- The `lmstudio-proxy` failure I mentioned earlier turned out to be machine state, not this PR and not main. It is #48.
- I opened #47 for the `nvpair-engine-manager` test that fails on macOS at main, which is the one from my first comment.
