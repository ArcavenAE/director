> **Corrected after filing.** My first version framed this as an oversight and proposed `ps -o comm=` as the fix. Both were wrong and are fixed below: `procImage`'s own doc comment documents the macOS decline as intended, which I quoted around rather than reading, and `ps -o comm=` is spoofable on macOS so it would be the wrong primitive. The executed evidence is unchanged. Apologies for the noise.

On macOS, `procImage` can never resolve an engine's executable path, so the ownership check in `doStop` always fails closed. The code intends that decline, and says so. What I would like to put in front of you is the cost of it on a platform where both shipping engines are supported, plus one concrete defect it causes today: `make test-services` is red on macOS.

Found while testing #9 on Apple Silicon. **This is not caused by #9**, and it is separate from the loopback-alias failure also visible on macOS. It reproduces at `origin/main` with the PR absent. Filed separately so #9 stays clean.

### PAIR version or commit

`13b68115fa2c9c1d94f1ead1358f8d5a527cfecf` (origin/main)

### Affected component

Engine or model management

### Environment

macOS on Apple Silicon (darwin/arm64), Go 1.26.5. Clean tree at the commit above.

### Steps to reproduce

```sh
git checkout 13b6811
cd services/nvpair-engine-manager
go test -run TestUninstallTerminatesRunningInstance -count=1 ./...
```

Reproduced on three consecutive runs, and at the head of #9.

### Expected behavior

The suite passes on macOS, or the test declares that it asserts Linux-only behavior and skips.

### Actual behavior

```
--- FAIL: TestUninstallTerminatesRunningInstance (1.40s)
    executor_test.go:706: uninstall: cannot uninstall engine "fake": cannot stop engine "fake":
    it is running under external management (pid 98257, ); stop it in its own application, then retry
```

Note the empty image field after the pid.

### Mechanism, and what is intended

`proc_unix.go` is built `//go:build !windows`, so it serves macOS and Linux together, and `procImage` is implemented for Linux only. Its doc comment states the consequence as a deliberate choice:

> macOS has no /proc, so it returns "" there and the image check is skipped — reclamation on macOS relies on the caller declining when the image can't be confirmed.

So the decline is intended. `isOurEngineImage` rejects an empty image explicitly, and `TestIsOurEngineImage` asserts that with a `{"", false}` case, so the fail-closed posture is deliberate at both layers. I should have read that comment before filing; my first version presented this as an accident.

Two things I would still raise:

**1. The test fails rather than skips.** If declining is correct on macOS, `TestUninstallTerminatesRunningInstance` is asserting Linux behavior on every platform. Today that leaves `make test-services` red on a supported development platform, which makes a genuine regression harder to see. A `t.Skip` on darwin, or an assertion that the decline happens there, would restore a clean baseline.

**2. The cost may be higher than the comment implies.** `manifests/lmstudio.json` and `manifests/ollama.json` both list `darwin/arm64` and `darwin/amd64`, so macOS is a first-class engine platform. On it, the path whose comment reads "so a user OFF actually takes effect instead of being refused forever" cannot run, so a PAIR-managed orphan on PAIR's own port stays refused for the life of the process. I have not reproduced that in the desktop app, so the user-facing impact is inferred from the code path rather than observed.

### On resolving the image without weakening the check

If lifting the decline on macOS is of interest, the primitive matters, and the obvious candidate is the wrong one. `ps -o comm=` on macOS reports an argv[0]-derived name, which a process can set freely:

<details>
<summary>Probe on darwin/arm64: ps is spoofable, lsof is not</summary>

```
$ ls /proc
ls: /proc: No such file or directory

# procImage copied verbatim from proc_unix.go, run on darwin:
os.Readlink(/proc/21344/exe) err = readlink /proc/21344/exe: no such file or directory
procImage(21344) = ""

# a process that lies about its name
$ (exec -a TOTALLY_FAKE sleep 8 &)

$ ps -o comm= -p 24461
TOTALLY_FAKE

$ lsof -p 24461 -a -d txt -Fn
p24461
ftxt
n/bin/sleep
```

</details>

`lsof -p <pid> -a -d txt -Fn` returns the executable the kernel actually mapped, not a name the process chose. It also adds no new dependency, since `pidOnPort` already shells out to `lsof` in this same file and already bounds it with `portLookupTimeout`. `proc_pidpath()` via libproc would be the more direct answer, but it needs cgo, and I could not find any `import "C"` in the tree, so I assume that is a deliberate posture.

Would a darwin `procImage` backed by `lsof -d txt`, plus a platform guard on the test, be a direction you would take? Happy to follow up with a pull request. If you would rather keep the decline on macOS as-is, the test guard alone is still worth having.

### Sanitized logs or screenshots

Included above; no credentials, node identities, or network details are involved.

### Confirmations

- [x] I searched existing issues for duplicates.
- [x] This is not a security vulnerability.
- [x] I agree to follow the Code of Conduct.
