`doStop` has a branch whose own comment explains its purpose: reclaim a PAIR-managed orphan on PAIR's own port "so a user OFF actually takes effect instead of being refused forever." On macOS that branch appears to be unreachable, because the ownership check it depends on is implemented against `/proc`, which macOS does not have. The engine identification returns an empty string, the check fails closed, and the reclaim is declined.

Found while testing #9 on Apple Silicon. **This is not caused by #9**, and it is not the loopback-alias test failure also visible on macOS; it reproduces at `origin/main` with the PR absent, and it is a different root cause from both. Filing it separately so #9 stays clean.

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

Reproduced on three consecutive runs, and also at the head of #9.

### Expected behavior

`doStop` recognizes a process on PAIR's own port as PAIR's own engine, terminates it, and lets the stop or uninstall proceed.

### Actual behavior

```
--- FAIL: TestUninstallTerminatesRunningInstance (1.40s)
    executor_test.go:706: uninstall: cannot uninstall engine "fake": cannot stop engine "fake":
    it is running under external management (pid 98257, ); stop it in its own application, then retry
```

Note the empty image field after the pid.

### Mechanism

This part is a reading of the source plus a probe of the primitive, not an instrumented run of the failing path itself.

`proc_unix.go` is built for `//go:build !windows`, so it covers macOS and Linux together, but `procImage` is implemented only for Linux:

```go
func procImage(pid int) string {
	if pid <= 0 {
		return ""
	}
	if path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); err == nil {
		return path
	}
	return ""
}
```

There is no `proc_darwin.go`. On macOS the readlink always fails, so `procImage` always returns `""`. `pidOnPort` returns that empty image, and `isOurEngineImage` rejects it explicitly:

```go
func isOurEngineImage(image, binPath string) bool {
	if image == "" || binPath == "" {
		return false
	}
	...
}
```

which makes the guard at `lifecycle.go:416` false on macOS for every engine, so the reclaim block never runs and control falls through to the "external management" error. `pidOnPort` has one non-test consumer, that branch, so the blast radius looks confined to it.

A related signal that the implementation was written against Linux: `normalizeEngineImage` strips a trailing `" (deleted)"`, which is a `/proc/<pid>/exe` artifact.

`ps -o comm= -p <pid>` returns the full executable path on macOS, so the same comparison looks reachable there without new dependencies.

<details>
<summary>Probe of the primitive on darwin/arm64</summary>

```go
// procImage copied verbatim from proc_unix.go
func procImage(pid int) string {
	if path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); err == nil {
		return path
	}
	return ""
}
```

```
$ ls /proc
ls: /proc: No such file or directory

$ go run procprobe.go
os.Readlink(/proc/21344/exe) err = readlink /proc/21344/exe: no such file or directory
procImage(21344) = ""
ps -o comm= -p 21344 = "/var/folders/.../exe/procprobe\n"
```

</details>

### Sanitized logs or screenshots

Included above; no credentials, node identities, or network details are involved.

I have reproduced the unit test failure and the underlying primitive, but I have not reproduced the user-facing symptom in the desktop app, so the practical impact on macOS users is inferred from the code path rather than observed.

Could `procImage` fall back to `ps -o comm=` on darwin, so the orphan-reclaim branch behaves the same on both Unix platforms? Happy to follow up with a pull request if that is the direction you would take.

### Confirmations

- [x] I searched existing issues for duplicates.
- [x] This is not a security vulnerability.
- [x] I agree to follow the Code of Conduct.
