// Some npm packages ship .go files (flatted/golang): a go.mod makes node_modules a module of its own,
// which `go build ./...`, `go vet ./...`, golangci-lint and `ginkgo -r` of the flock module skip.
import { writeFileSync } from 'node:fs';

writeFileSync(new URL('../node_modules/go.mod', import.meta.url), 'module ignored/node_modules\n');
