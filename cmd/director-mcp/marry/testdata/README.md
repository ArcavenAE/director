# Pinned vector files

These two files are byte copies of marvel's `contracts/readiness` vectors, taken
(routing-vectors.json at marvel commit 76cd695, marry-vectors.json at marvel commit e4a7a17, which adds the cluster to pairs, leases and unplaced rows). They are the shared test of the routing rule and the
marrying step, so director and marvel agree on one answer without either
importing the other.

| file | sha256 |
|---|---|
| marry-vectors.json | 5fe464863800eb75911fb2f4b5e5c8190678057480e2b4801f08bebead92376f |
| routing-vectors.json | 4cb6a7295845ace0a9456d8723013675907b0def798b016ad7e265ad540a6be4 |

`vectors_test.go` asserts both hashes before it reads a file. A changed vector is
a changed contract: re-pin only on purpose, in a PR that says so, and update the
hashes here and in the test together.

The `grep` steps in the marry vectors read marvel's own state, so this package
skips them and counts the skips.
