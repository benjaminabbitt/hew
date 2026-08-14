# hew task runner. Implementations register their targets here; the corpus is
# the standard, so every implementation's test target must drive corpus/.

default: check

# Validate the corpus tree's internal consistency (case layout, README index).
# Cheap structural check only — full validation is each implementation's runner.
check:
    @test -d corpus && test -f docs/hew-spec.md && echo "layout ok: $(find corpus -type d -mindepth 1 -maxdepth 1 | wc -l) corpus families, $(find corpus -type f | wc -l) files"

# Go implementation (go/): library + hew CLI.
test-go:
    @test -f go/go.mod || { echo "go implementation not started (go/go.mod missing)"; exit 1; }
    cd go && go test ./...

corpus-go:
    @test -f go/go.mod || { echo "go implementation not started (go/go.mod missing)"; exit 1; }
    cd go && go test ./... -run TestCorpus -v

# Rust implementation (rust/): planned port; same corpus.
test-rust:
    @test -f rust/Cargo.toml || { echo "rust implementation not started (rust/Cargo.toml missing)"; exit 1; }
    cd rust && cargo test

corpus-rust:
    @test -f rust/Cargo.toml || { echo "rust implementation not started (rust/Cargo.toml missing)"; exit 1; }
    cd rust && cargo test corpus
