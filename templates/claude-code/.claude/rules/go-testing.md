---
description: Conventions for Go test files. Applies when writing or editing tests under internal/.
paths: internal/**/*_test.go
---

# Go testing conventions

Follow these when writing or editing `*_test.go` files. They reflect the stdlib `testing` package and testify/testifylint best practice.

## Structure & naming

- Keep tests beside the code: `foo_test.go` next to `foo.go`.
- Prefer the **black-box** package `foo_test` so tests exercise the public API. Use the same package `foo` only when a test genuinely needs unexported internals.
- Test functions are `func TestXxx(t *testing.T)`; benchmarks `BenchmarkXxx(b *testing.B)`; fuzz `FuzzXxx(f *testing.F)`.

## Table-driven tests + subtests

- Default to table-driven tests with a slice of case structs, each with a `name`, run via `t.Run(tc.name, func(t *testing.T) { ... })`.
- One assertion focus per case; the `name` must describe the scenario, not the input.

```go
tests := []struct {
    name    string
    in      string
    want    int
    wantErr bool
}{
    {name: "empty input", in: "", want: 0},
    {name: "valid", in: "42", want: 42},
}
for _, tc := range tests {
    t.Run(tc.name, func(t *testing.T) {
        t.Parallel()
        got, err := Parse(tc.in)
        if (err != nil) != tc.wantErr {
            t.Fatalf("Parse(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
        }
        if got != tc.want {
            t.Errorf("Parse(%q) = %d, want %d", tc.in, got, tc.want)
        }
    })
}
```

## Lifecycle & helpers

- Call `t.Helper()` at the top of any assertion/setup helper so failures report the caller's line.
- Use `t.Cleanup(fn)` for teardown instead of `defer` — it runs even for subtests and composes cleanly.
- Use `t.TempDir()` for filesystem fixtures (auto-removed) and `t.Context()` (Go 1.24+) for a context cancelled when the test ends.
- Use `TestMain(m *testing.M)` only for genuinely package-wide setup/teardown; call `m.Run()` and exit with its code.

## Parallelism & isolation

- Add `t.Parallel()` to independent tests and subtests to surface races and speed runs.
- Never share mutable global state between parallel tests. Don't rely on execution order.
- Never use `time.Sleep` for synchronization — use channels, `sync`, or `t.Context()`.

## Assertions

**Standard library** (preferred for simple cases): compare and report with got/want ordering. Use `t.Fatal*` to stop the test, `t.Error*` to record and continue.

```go
if got != want {
    t.Errorf("Sum() = %v, want %v", got, want)
}
```

For structs/slices/maps use `go-cmp`, not `reflect.DeepEqual`:

```go
if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("mismatch (-want +got):\n%s", diff)
}
```

**testify** (when the project already uses it — be consistent within a file):

- `require.*` halts on failure (use for preconditions and inside goroutines where continuing is unsafe); `assert.*` records and continues.
- Argument order is `(t, expected, actual)`: `assert.Equal(t, want, got)`.
- Errors: `require.NoError(t, err)` / `assert.Error(t, err)` — never `assert.Nil(t, err)` / `assert.Equal(t, nil, err)`.
- Error identity/type: `require.ErrorIs(t, err, ErrSentinel)` or `errors.As` — never string-compare messages.
- Nil: `assert.Nil/NotNil`, not `assert.Equal(t, nil, x)`.
- Emptiness: `assert.Empty(t, slice)`, not `assert.Len(t, slice, 0)` or `assert.Equal(t, 0, len(slice))`.
- Floats: `assert.InDelta` / `assert.InEpsilon`, never `assert.Equal` on float values.
- Enable the `testifylint` linter (golangci-lint) to enforce the above automatically.

## Mocks

- Mock through interfaces the production code depends on; keep mock definitions in the test package.
- With testify mocks, set expectations via `m.On("Method", args...).Return(...)` (use `mock.Anything` for unpredictable args) and verify with `m.AssertExpectations(t)`.

## Fuzzing

- Add `FuzzXxx` with `f.Add(seed)` corpus entries for parsers, decoders, and any input-handling boundary; assert invariants (round-trip, no panic) inside `f.Fuzz`.

## Running

- The expected commands are `go test ./... -race -cover` for the suite and `go test -run TestName ./internal/pkg` for a single test. Tests must pass under `-race`.
