---
description: Rules and conventions for the rivo/tview terminal UI layer — the Application, primitives, layout, and the concurrency model that keeps the screen race-free.
paths:
  - internal/tui/**
---

# tview TUI rules (`internal/tui`)

These rules apply when working in `internal/tui` — the terminal user-interface layer built on
`rivo/tview` (which sits on top of `gdamore/tcell/v2`).

## Library

- **Package:** `github.com/rivo/tview` — rich, interactive terminal widgets. Built on
  `github.com/gdamore/tcell/v2` (the screen/event backend).
- **Version:** pin the latest stable `rivo/tview` and `gdamore/tcell/v2` in `go.mod`. tcell **v2**
  is mandatory — never import the v1 module path.
- **Imports:** no alias; use the canonical names:
  ```go
  import (
      "github.com/gdamore/tcell/v2"
      "github.com/rivo/tview"
  )
  ```
- **Docs:** README https://github.com/rivo/tview · GoDoc https://pkg.go.dev/github.com/rivo/tview ·
  Wiki (Concurrency, Primitives, Pages, Form, TextArea) https://github.com/rivo/tview/wiki

## The application & its single goroutine (the rule that matters most)

tview has **one event loop, on one goroutine**. Almost nothing in tview is thread-safe. Everything
below follows from this.

- Create exactly **one** `*tview.Application` (`tview.NewApplication()`), own it in `internal/tui`,
  and start it with `app.SetRoot(root, true).EnableMouse(true).Run()`. `Run()` blocks until
  `app.Stop()`; propagate its returned error to the caller — never `panic` in library code.
- **Event handlers run on the main goroutine.** Callbacks like `SetSelectedFunc`,
  `SetChangedFunc`, `SetDoneFunc`, `SetInputCapture`, and button handlers are safe to mutate
  primitives directly — do **not** wrap them in `QueueUpdate`/`QueueUpdateDraw`.
- **Never call `app.Draw()`, `app.QueueUpdate()`, or `app.QueueUpdateDraw()` from inside an event
  handler or `SetInputCapture`** — the loop already redraws after the handler. Doing so deadlocks.

## Updating the UI from background goroutines

Any goroutine other than the event loop must funnel mutations through the queue:

- **`app.QueueUpdateDraw(func(){ ... })`** — run the closure on the event loop **and** redraw.
  This is the default for "data changed, refresh the screen". It blocks until the closure has run.
- **`app.QueueUpdate(func(){ ... })`** — same synchronization without the implicit redraw; use for
  batched changes (and for read-only access to a primitive another goroutine may mutate).
- Keep queued closures **tiny** — just the primitive mutation. Do the slow work (I/O, DB calls,
  computation) in the goroutine *before* queuing, never inside the closure (it stalls the UI).
- **Never block the event loop**: no network/disk/DB work in handlers or queued closures. Run it in
  a goroutine and `QueueUpdateDraw` the result back.

```go
go func() {
    products, err := repo.List(ctx) // slow work OFF the event loop
    app.QueueUpdateDraw(func() {     // tiny mutation ON the event loop
        if err != nil {
            statusBar.SetText("[red]load failed")
            return
        }
        renderTable(table, products)
    })
}()
```

### TextView is the one exception

`*tview.TextView` implements `io.Writer` and its writes are goroutine-safe. You may
`fmt.Fprintf(textView, ...)` from any goroutine for logs/streaming output. To repaint as it fills,
set `SetChangedFunc(func(){ app.Draw() })`. Note: unlike every other handler, **`SetChangedFunc`
runs on the *writer's* goroutine, not the event loop** — which is exactly why the correct call
there is `app.Draw()` (safe from any goroutine), and you must *not* mutate other primitives inside
it.

## Layout & composition

- Compose the UI as a tree of **layout primitives wrapping leaf widgets**. Prefer:
  - **`Flex`** for proportional row/column layouts: `AddItem(p, fixedSize, proportion, focus)` —
    `fixedSize 0` + `proportion > 0` means "share the remaining space".
  - **`Grid`** for fixed grids with `SetRows`/`SetColumns` (`0` = flexible track).
  - **`Pages`** for stacked screens, modals, and overlays — switch with
    `SwitchToPage` / `ShowPage` / `HidePage`.
- Keep `SetRoot` called **once** with the top-level container; swap *content* via `Pages`, not by
  repeatedly calling `SetRoot`.
- Build each screen/widget as its own constructor (e.g. `newProductList(app, repo) *tview.Flex`)
  that wires its handlers and returns the configured primitive. No package-level mutable UI state.

## Widgets — pick the right one

- **`Table`** — spreadsheet/grid data (the product list). `SetCell(row, col, NewTableCell(...))`,
  `SetFixed(rows, cols)` to freeze headers, `SetSelectable(rows, cols)` for row/cell selection,
  `SetSelectedFunc` (Enter on a row) and `SetDoneFunc` (Escape). Remember header rows shift your
  data indices — guard against selecting row 0.
- **`List`** — single-column menus with optional shortcut runes and secondary text.
- **`TreeView`** — hierarchical data; build with `NewTreeNode`, `AddChild`, `SetReference` to attach
  your domain object, expand/collapse via `SetExpanded`, handle `SetSelectedFunc`.
- **`Form`** — stacked inputs + buttons; add `InputField`, `DropDown`, `Checkbox`, `TextArea`,
  `PasswordField`, then read values with `GetFormItemByLabel("X").(*tview.InputField).GetText()`.
- **`InputField`** — single-line input. Constrain with `SetAcceptanceFunc` (e.g.
  `tview.InputFieldInteger`), suggest with `SetAutocompleteFunc`, mask with `SetMaskCharacter`.
- **`Modal`** — confirmation/alert dialogs; layer over content via `Pages`.

### Large datasets: virtual tables

Do **not** `SetCell` millions of rows — it holds every cell in memory. For large/streamed data,
implement the **`TableContent`** interface (`GetCell`, `GetRowCount`, `GetColumnCount`) and attach
it with `table.SetContent(content)`; tview calls it only for the **visible** rows. Embed
`tview.TableContentReadOnly` for read-only data. Leave `SetEvaluateAllRows(false)` (the default) —
enabling it defeats the point.

## Focus & input

- The Application tracks a single focused primitive. Set it explicitly with `app.SetFocus(p)`;
  after changing pages or rebuilding a view, restore focus deliberately.
- Use **`app.SetInputCapture`** for global keybindings (e.g. Ctrl-C / quit, page switching) and a
  primitive's `SetInputCapture` for local ones. Return `nil` to consume an event, or return the
  `*tcell.EventKey` to let it propagate.
- Compare keys via `event.Key()` (`tcell.KeyEnter`, `tcell.KeyEscape`, `tcell.KeyCtrlC`, …) and
  `event.Rune()` for printable keys. Always provide a visible, documented way to quit.

## Mouse, paste & suspending the UI

- Enable mouse and bracketed paste at startup when the UI benefits:
  `app.SetRoot(root, true).EnableMouse(true).EnablePaste(true)`. Without `EnablePaste`, pasted
  multi-line text arrives as individual keystrokes.
- To run an external program or drop to the shell (e.g. an `$EDITOR`), use **`app.Suspend(fn)`** —
  it restores the normal terminal, runs `fn`, then re-enters the TUI. Never spawn a foreground
  subprocess without it, or it will fight tview for the terminal.

## Testing

- tview is testable headless: create the app, build a `tcell.SimulationScreen`
  (`tcell.NewSimulationScreen("UTF-8")`, then `SetSize`), attach it with **`app.SetScreen(sim)`**,
  drive input, and assert on `sim.GetContents()`. No real terminal required.
- Keep rendering logic in pure helpers (data → cells/strings) that you can unit-test *without* the
  Application at all; reserve screen-level tests for wiring/keybindings.

## Custom primitives

- Build a custom widget by **embedding `*tview.Box`** and implementing only the `Primitive` methods
  you need (`Draw`, `InputHandler`, …); embedding `Box` supplies sane defaults for the rest.
  ```go
  type ProductPanel struct {
      *tview.Box
      // fields...
  }
  func NewProductPanel() *ProductPanel { return &ProductPanel{Box: tview.NewBox()} }
  ```

## Theming & text markup

- Set the global theme **once at startup, before any widget is created**, by assigning
  `tview.Styles = tview.Theme{...}`. Don't hard-code per-widget colors that fight the theme.
- Enable inline color tags per widget with `SetDynamicColors(true)`, then use tcell-style tags:
  `[fg:bg:attrs:url]` (e.g. `[red]`, `[yellow:blue]`, `[::b]bold[::-]`, `[-:-:-:-]` full reset).
  Escape a literal tag with a trailing `[` : `[red[]` prints `[red]`.

## Do / Don't

- ✅ Own one `Application`; return `Run()`'s error up the stack.
- ✅ Do slow work in a goroutine, then `QueueUpdateDraw` a **small** mutation back.
- ✅ Mutate freely inside event handlers — they're already on the main goroutine.
- ✅ Keep `internal/tui` a thin view layer; pull data through `internal/db` repositories, never open
  bbolt or run business logic in a draw/handler.
- ❌ Don't call `Draw`/`QueueUpdate`/`QueueUpdateDraw` from within an event handler or input capture
  (deadlock).
- ❌ Don't touch a primitive from a goroutine outside the queue (race) — except writing to a
  `TextView` via `io.Writer`.
- ❌ Don't block the event loop with I/O, DB, or network calls.
- ❌ Don't `panic` on UI errors in library code; surface them to the caller or a status widget.
