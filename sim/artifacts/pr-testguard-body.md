`make test-services` is red on macOS at `main`, which makes a real regression harder to notice on a platform where both shipping engines are supported. This makes the one failing test skip on the precondition it actually needs, so the baseline is green again without changing any behavior.

`TestUninstallTerminatesRunningInstance` asserts that `Uninstall` reclaims a managed engine started without an executor process handle. That reclaim needs the listener's executable path, and `procImage` resolves it through `/proc`. Where there is no `/proc` the image comes back empty, `isOurEngineImage` declines, and `Uninstall` refuses. `procImage`'s own doc comment describes that as the intended behavior:

> macOS has no /proc, so it returns "" there and the image check is skipped — reclamation on macOS relies on the caller declining when the image can't be confirmed.

So the test was asserting the Linux outcome everywhere, and failing rather than skipping where the decline is by design.

The guard checks the precondition rather than the platform:

```go
if _, image, ok := pidOnPort(port); ok && image == "" {
    t.Skipf("listener image is unresolvable on %s, so reclaim declines by design", runtime.GOOS)
}
```

A `GOOS == "darwin"` check would go stale the moment the image becomes resolvable there. This one starts running again on its own, and it skips only on the exact documented condition, a resolved pid with no image, so a genuine lookup failure still reaches the assertion.

**In scope:** one test file, one guard. **Out of scope:** whether the decline itself should be lifted on macOS, which is #17 and is yours to rule on. This PR is useful either way, and does not presume that answer.

Checks run on darwin/arm64, Go 1.26.5, at `13b6811`:

```
go test -run TestUninstallTerminatesRunningInstance -count=1 -v ./...
  --- SKIP: TestUninstallTerminatesRunningInstance (0.52s)
      executor_test.go:701: listener image is unresolvable on darwin, so reclaim declines by design
  ok  nvpair-engine-manager

go test -count=1 ./...
  ok  nvpair-engine-manager  37.073s

gofmt -l services/nvpair-engine-manager/   (clean)
go vet ./...                               (clean)
node scripts/spdx-headers.mjs
  871 checked, 0 missing, 0 to review, 0 unclassified, 94 skipped
```

I could not run the Linux path, so the claim that the test still executes there rests on `procImage` returning a real path from `/proc/<pid>/exe`, not on an observed run.

No service binary output changes, so no `services/versions.json` bump.

Refs #17
