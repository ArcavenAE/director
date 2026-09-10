The Go suite writes into the live per-user application-data directory, so running it changes the state of the developer's own PAIR installation and then breaks a later run of itself. Concretely, `services/nvpair-ui-broker`'s tests write a real `lmstudio-proxy-port.json`, and from that point `TestE2EFailoverOverRealBinary` in `services/lmstudio-proxy` fails on that machine until the file is removed by hand.

The shape is worth naming because it hides: the suite passes on a clean machine and fails from the second run onward, and the two modules are separate, so neither one is wrong when read on its own. `lmstudio-proxy` sorts before `nvpair-ui-broker`, so a single `make test-services` run poisons the *next* one rather than itself.

Found while testing #27 on Apple Silicon. Not caused by that PR, and not caused by #9; it reproduces at `main` with no PR applied. I first mistook it for a platform failure and reported it internally as one, which is the other reason to file it: from inside a tree it looks like a real regression.

### PAIR version or commit

`13b68115fa2c9c1d94f1ead1358f8d5a527cfecf` (origin/main), clean tree

### Affected component

Services / broker

### Environment

macOS on Apple Silicon (darwin/arm64), Go 1.26.5. The directory is the one `services/shared/appdir` resolves, which on macOS is `~/Library/Application Support/Nvidia Corporation/Personal AI Router`. The same leak should apply on Linux and Windows at their equivalents, though I have only tested macOS.

### Steps to reproduce

<details>
<summary>One fresh top-to-bottom run at 13b6811</summary>

```
$ git rev-parse HEAD
13b68115fa2c9c1d94f1ead1358f8d5a527cfecf
$ git status --porcelain   # (empty)

$ rm -f "$APPDIR/lmstudio-proxy-port.json"   # start from a clean machine

$ cd services/lmstudio-proxy && go test -run TestE2EFailoverOverRealBinary -count=1 ./...
ok  	lmstudio-proxy	0.924s

$ cd services/nvpair-ui-broker && go test -count=1 ./... >/dev/null   # a different module

$ cat "$APPDIR/lmstudio-proxy-port.json"   # real user state, now written
{"port":1240}

$ cd services/lmstudio-proxy && go test -run TestE2EFailoverOverRealBinary -count=1 ./...   # same test, same tree
--- FAIL: TestE2EFailoverOverRealBinary (0.29s)
    e2e_test.go:187: ready port = 1240, want 63118
FAIL
```

`$APPDIR` is `~/Library/Application Support/Nvidia Corporation/Personal AI Router`.

</details>

### Expected behavior

Tests read and write a temporary directory, so a run neither depends on nor alters the developer's installed PAIR state, and the suite is repeatable.

### Actual behavior

`services/nvpair-ui-broker` writes `lmstudio-proxy-port.json` into the live directory. `TestE2EFailoverOverRealBinary` launches the real binary, which restores that persisted port instead of the one the test assigned, and fails with `ready port = 1240, want <assigned>`. It then fails on every subsequent run until the file is deleted. I confirmed the direction both ways: with the file present it failed five times out of five across `main`, #9, and #27; with it moved aside it passed every time.

### Mechanism

`main.go` calls `loadPersistedPort()` and prefers the persisted value over `--port` unless `--ignore-persisted-port` is passed, and `savePersistedPort` writes through `appdir.Path`, which resolves the real per-user directory rather than anything test-scoped. Nothing in that path is redirected during tests.

`services/tests` leaks into the same directory: today's run updated `workloads-history.json` there. I did not audit which other files or modules are affected, so the two I name are examples rather than the full list.

### Sanitized logs or screenshots

Included above. No credentials, node identities, or network details are involved.

Would an `appdir` base that honors an environment override, set by the test harness to `t.TempDir()`, be the direction you would want? That would fix every consumer at once rather than per-test. Happy to follow up with a pull request. `--ignore-persisted-port` in the affected test would also unblock it, though it leaves the underlying leak in place.

### Confirmations

- [x] I searched existing issues for duplicates.
- [x] This is not a security vulnerability.
- [x] I agree to follow the Code of Conduct.
