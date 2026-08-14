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

# Acceptance criteria only (godog over features/, bound to the Go corpus runner).
accept-go:
    @test -f go/go.mod || { echo "go implementation not started (go/go.mod missing)"; exit 1; }
    cd go && go test ./conformance -run TestFeatures -v

# Corpus with the skip registry disallowed — the end-state conformance gate.
corpus-go-strict:
    @test -f go/go.mod || { echo "go implementation not started (go/go.mod missing)"; exit 1; }
    cd go && HEW_CORPUS_NO_SKIPS=1 go test ./... -run TestCorpus -v

# Mutation testing, unit-test killers only (fast inner loop; slow suites skipped).
mutate-go pkg="./internal/...":
    @test -f go/go.mod || { echo "go implementation not started (go/go.mod missing)"; exit 1; }
    cd go && HEW_SKIP_SLOW_SUITES=1 go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash --timeout-coefficient 30 --invert-assignments --invert-logical --invert-loopctrl {{pkg}}

# Mutation with the corpus + acceptance suites as killers (milestone exit gate; slow).
mutate-go-acceptance pkg="":
    @test -f go/go.mod || { echo "go implementation not started (go/go.mod missing)"; exit 1; }
    cd go && go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash --coverpkg ./... --integration --timeout-coefficient 4 --invert-assignments --invert-logical --invert-loopctrl {{pkg}}

# Rust implementation (rust/): planned port; same corpus.
test-rust:
    @test -f rust/Cargo.toml || { echo "rust implementation not started (rust/Cargo.toml missing)"; exit 1; }
    cd rust && cargo test

corpus-rust:
    @test -f rust/Cargo.toml || { echo "rust implementation not started (rust/Cargo.toml missing)"; exit 1; }
    cd rust && cargo test corpus
