# E: AI-First Editor Roadmap for 2027

E should remain a fast, native GNOME editor rather than growing into a clone of
VS Code. The product model is three direct interactions:

- **Tab — Predict:** low-latency completion and next-edit prediction.
- **Inline action — Transform:** rewrite a selection or file with a reviewable diff.
- **Agent panel — Delegate:** ask, plan, edit, run, verify, and review a larger task.

Status: **done**, **in progress**, **next**, or **later**.

## Native editor foundation

- **done** GTK 4/libadwaita shell with GtkSourceView syntax highlighting.
- **done** Chunked, non-blocking loading and background atomic saves.
- **done** Search, replace, spell checking, EditorConfig, and session recovery.
- **done** VS Code-style multiple cursors and keyboard bindings.
- **done** Resizable, collapsible, lazily scanned project tree.
- **done** Ordered, availability-aware local/cloud model chain and keyring storage.
- **done** Command Palette (`Ctrl+Shift+P`) with fuzzy command filtering.
- **done** Quick Open (`Ctrl+P`) with asynchronous workspace discovery.
- **done** Live Markdown preview (`Ctrl+Shift+V`).
- **done** Persistent bottom status bar with document state, language, encoding,
  line ending, cursor position, operation progress/results, activity history,
  and visible serving-model/fallback status.
- **done** Buffer-independent completion renderer that draws beside a line-end
  caret or in reserved space below the paragraph, never over typed text.
- **done** Incremental standalone `E` branding with accent color and italics.
- **done** Explorer create, rename, delete, and refresh actions.
- **next** Explorer drag/drop, automatic file watching, and `.gitignore` filtering.
- **next** Go to line, symbols, breadcrumbs, outline, folding, split editors, and
  pinned/preview tabs.
- **next** Complete LSP navigation: definition, references, rename, hover,
  formatting, signature help, diagnostics, and code actions.
- **next** Git diff gutter, Source Control view, history, blame, and changed-files
  review.
- **next** Integrated terminal, Problems panel, task runner, debugger adapter, and
  test explorer.
- **later** Profiles, editable keymap, themes, remote workspaces, and real-time
  collaboration.

## Tab — prediction

- **done** Debounced inline ghost text with cancellation and bounded context.
- **done** Automatic failover across every configured model, including Qwen and
  Lemonade/Gemma for offline use.
- **next** FIM-native provider profiles and capability-aware routing per feature.
- **in progress** Safe multi-line predictions use a reserved below-line preview;
  language-aware syntax colors remain next.
- **next** Partial word and line acceptance.
- **next** Next-edit prediction: predict both the edit and its location, then use
  Tab to navigate between suggested edits.
- **next** Cross-file predictions using open files, recent edits, imports,
  diagnostics, and symbols.
- **next** Speculative local completion: ask a tiny local model immediately while
  a stronger model races only when it can beat the latency budget.
- **later** Private local learning from accepted/rejected suggestions and adaptive
  eager/subtle presentation based on confidence.

## Inline action — transformation

- **in progress** Prompt bar for selected text, current symbol, or current file;
  selection transformation is available from the Command Palette.
- **in progress** Reviewable replacement preview instead of directly modifying the
  buffer; streaming and per-hunk presentation remain next.
- **next** Inline diff with keep/reject controls per hunk and one semantic undo.
- **next** Built-in intents: rewrite, explain, document, refactor, generate tests,
  fix diagnostics, change tone, summarize, and translate.
- **next** Prompt history and reusable project/user actions.
- **later** Multi-file transformations with dependency-aware edit ordering.

## Agent panel — delegation

- **next** Resizable agent panel with Ask, Plan, and Agent modes.
- **next** Streaming conversations with editable prompts, steering, queues, and
  model selection.
- **next** Native tools: read, search, edit, create, rename, delete, terminal,
  diagnostics, tests, Git, and workspace symbols.
- **next** Permission profiles: read-only, confirm writes, assisted, and trusted.
- **next** Checkpoint before every agent turn; restore or fork from any checkpoint.
- **next** Changed-files summary and multi-file/per-hunk review.
- **next** Agent verification loop that runs focused tests and responds to failures.
- **next** Isolated Git worktrees for safe background and parallel agents.
- **later** Desktop notifications, queued follow-ups, scheduled agents, and remote
  sandboxes.

## Context and intelligence

- **in progress** Workspace file index shared by Quick Open and future agents.
- **next** Hybrid relevance engine using filenames, ripgrep, LSP symbols, imports,
  AST structure, Git diff, open files, and recent edits before optional embeddings.
- **next** `@file`, `@folder`, `@symbol`, `@git`, `@diagnostics`, `@terminal`, and
  `@web` context references.
- **next** Context inspector showing exactly what will be sent to a model and why.
- **next** Respect `AGENTS.md`, user instructions, and scoped `.e/rules` files.
- **next** Local embedding/index option with incremental invalidation.
- **later** Project memory with explicit, inspectable, editable facts and decisions.

## Models, tools, privacy, and safety

- **done** Versioned E User-Agent, HTTPS validation, explicit local HTTP permission,
  and API keys outside configuration files.
- **next** Separate ordered model routes for prediction, transformation, planning,
  agent work, embeddings, vision, and summarization.
- **next** Model capability detection: FIM, tools, vision, context size, structured
  output, latency, and offline availability.
- **next** Air-gapped mode that disables every non-loopback request.
- **next** Secret and sensitive-file redaction with `.eignore` support.
- **next** Per-model privacy dashboard, request log, token/latency metrics, and
  one-click disable.
- **next** MCP tools and ACP-compatible external agent processes.
- **next** Sandboxed terminal commands with readable approval details and durable,
  narrowly scoped permissions.
- **later** Multimodal prompts from pasted screenshots, diagrams, PDFs, and UI
  selections.

## Performance requirements

- Keep typing, scrolling, search, file discovery, rendering, and AI networking off
  each other's critical paths.
- Maintain explicit latency budgets: local keystroke handling under 8 ms, visible
  completion target under 150 ms, and cancellable work everywhere.
- Never send full large files when a bounded semantic context will work.
- Add benchmarks for 5 MB, 50 MB, and 500 MB inputs; investigate a rope/piece-table
  buffer if GtkTextBuffer becomes the limiting factor.
- Make indexes incremental, content-addressed, bounded on disk, and disposable.
- Degrade gracefully: turn off expensive highlighting, LSP, preview, or indexing
  independently instead of stalling the editor.

## Recommended delivery order

1. Command Palette, Quick Open, Markdown preview, and complete Explorer actions.
2. Workspace context index and full LSP navigation.
3. Inline transformation with streaming diff and checkpoints.
4. Agent panel with safe native tools and verification.
5. Next-edit and cross-file prediction.
6. Git review, terminal, MCP/ACP, and isolated background agents.
7. Local personalization, collaboration, and multimodal workflows.
