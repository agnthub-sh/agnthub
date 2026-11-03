# large

One agent beside about 24 MiB of files that are not agents (`vendor/`, and a binary
above the per-file limit in `assets/`), which the suite generates when it publishes this
fixture (`writeLargeContent` in `gitserver_test.go`). A whole fetch exceeds the CLI's
budget, so the CLI fetches the trees and then only the files discovery reads.
