# Website Engine agent guidance

## Application map

- The admin is a React 18 + TypeScript SPA in `frontend/admin/src`, built with Vite and served below `/admin/` by the Go engine.
- Routes and role boundaries live in `frontend/admin/src/app/router.tsx`; the shared shell is `frontend/admin/src/app/layouts/AppShell.tsx`.
- API request code is centralized in `frontend/admin/src/api.ts`. Frontend types in `frontend/admin/src/types.ts` mirror the Go contracts. The handlers and role gates in `presentation/http/routes.go` are authoritative.
- `frontend/admin/src/domains/` holds reusable domain logic; `frontend/admin/src/pages/` holds route screens; `frontend/admin/src/components/ui/` holds admin Radix-based controls.
- `pkg/runtime/` owns public HTML templates, browser feature-lifecycle scripts, isolated public styles, and assets. `frontend/admin/` remains the React SPA; `frontend/ui/` contains admin styles. Never share admin CSS with public pages.
- Admin routing is declared in `frontend/admin/src/app/router.tsx`; public route matching and every initial HTML document are owned by the Go snapshot router/document renderer. Browser scripts only execute existing feature ABIs and use `/api/v2/runtime` for subsequent route changes. See [docs/FRONTEND_ARCHITECTURE.md](docs/FRONTEND_ARCHITECTURE.md) before changing route behavior or package boundaries.
- Chromium prerender stores fully rendered feature markup in route HTML. On normal browser loads, keep that markup available for an initial paint before mounting the existing feature ABI; do not change lifecycle signatures or add a client router to improve startup timing. Feature Lab is a separate loopback source-preview tool in `feature-lab/`, launched with the Vitasil frontend toolchain.

## Product invariants

- Keep existing CMS capabilities, user data, and role checks. The current model uses statically configured branches instead of fixed dev/prod environments; snapshot assignments are managed through `/snapshots/branches`. Preserve backend contracts unless the user's task or an approved specification explicitly requires changing them. Do not bypass or weaken route guards or the `can*` permission helpers.
- Keep destructive confirmations and editor unsaved-change protection.
- Preserve revision checks, content autosave, independent branch assignments, and publication/rollback safeguards. The current snapshot and branch model is defined in the TODO and its linked specifications; keep implementation and those contracts aligned.
- Do not send writes to a running API during visual QA unless it is an isolated test environment with disposable data.
- Do not add a UI, state, or test dependency without a concrete need and user approval.
- Keep `/site/**` focused on page, route, feature-placement, feature arguments, the graphical data model, and structural mapping from model fields to typed feature inputs. All feature arguments are configured only in `/site`; this includes selecting an existing media file when it is an argument, but uploads remain in `/media`. `/site` never edits model record values. `/content` generates record editors from the selected snapshot's `dataModel` and owns record drafts/publication plus editorial record selection/order for placements. Branches and snapshot-to-branch assignments are managed only in `/snapshots/branches`. Choosing a snapshot in `/site` is not branch management.
- Model entities have `many` or `singleton` cardinality. A new singleton gets exactly one empty draft record in its snapshot and is not published automatically. `/content` shows one record form; confirmed clear keeps the record ID. The server rejects cardinality changes that would discard records, and typed singleton `record`/`scalar` bindings resolve automatically while `collection` remains an array of zero or one records.
- Approved role matrix: `admin` and `moderator` manage site structure, feature builds, and branch assignments. All four roles (`admin`, `moderator`, `staff`, `user`) can read, edit, save, publish, and restore model content; all can read/export submissions, while only `admin`/`moderator` can change status or archive and only `admin` can delete; all can view/upload/rename media and use the picker, while only `admin` can permanently delete media. Keep server guards, settings role cards, frontend `can*` helpers, and visible controls aligned; denied requests must not leave partial changes.

## Disposable resource cleanup

- Record the exact names, ports, database names, buckets, volume names, and temporary paths created for each QA run. Before a destructive test, confirm the port owner and verify that the selected disposable database/storage has no active users.
- After the relevant checks finish, remove task-owned disposable databases, S3/MinIO buckets and objects, test product instances, containers, volumes, generated outputs, and temporary files/directories that are no longer needed. Target only recorded exact identifiers or paths; do not use broad wildcards or global `docker prune`/`docker compose down` commands.
- Remove a container or volume only after confirming it belongs to this task's isolated QA environment and no remaining test or application uses it. Preserve the working Vitasil stack, user data, and every resource whose ownership or purpose is uncertain; report uncertain leftovers instead of deleting them.

## Plan and specifications

- Track relevant implementation work in [docs/TODO-admin-snapshots-content-files.md](docs/TODO-admin-snapshots-content-files.md). Read its current decisions and open questions before work; the newer branch model supersedes historical dev/prod descriptions and checks.
- Update the relevant TODO items as work progresses. Mark an item complete only when its acceptance criteria are implemented and verified; record partial work, commands, failures, and environment limitations without presenting them as a pass.
- For substantial changes, use spec-driven development: document the intended behavior, invariants, data and API contracts, failure and concurrency cases, permissions, and acceptance tests before implementation. Keep the specification and documentation aligned with the delivered behavior.
- Treat [docs/TOPLAN.md](docs/TOPLAN.md) as proposals to evaluate against the agreed TODO, not as proof of implementation. Resolve material product ambiguities with the user; document smaller implementation decisions.

## Implementation and verification sequence

- For the active end-to-end Website Engine/Vitasil TODO goal, complete all applicable implementation and documentation work first. Defer typecheck, lint, build, unit, integration, browser, end-to-end, Vitasil, and release checks until the implementation sweep is complete; then run the planned verification pass, fix failures, and repeat only the checks needed to establish the fixes. This ordering supersedes earlier verification-first sequencing for this goal.
- Keep TODO checkboxes open during implementation. The coordinator marks an item complete only after the deferred checks verify its acceptance criteria and the QA report records the evidence.
- Do not invoke a Cloudflare skill as a recurring or mandatory implementation/verification gate. Cloudflare work is opt-in and starts only on the user's explicit request. If priorities change during an explicitly requested Cloudflare audit, stop new audit checks, preserve completed evidence, and record the exact paused phase and remaining scope.
- Preserve existing test results as evidence for unchanged code; do not rerun checks merely because the task spans multiple turns. Do not claim that deferred or interrupted checks passed.

## Parallel TODO execution

- When the user requests end-to-end TODO implementation or parallel work, delegate independent implementation packages to subagents without asking for that authorization again. First inspect active agents and the current git state so existing work is continued rather than duplicated. Verification normally belongs to each package, but when the current user instruction explicitly defers checks until the implementation sweep is complete, do not run package-level tests/checks; report the exact deferred commands for the coordinator's later QA phase.
- Decompose the remaining TODO into acceptance-sized packages. Give each agent a clear owner, exact file or directory boundaries, relevant specs, dependencies, acceptance criteria, and required checks. Agents should implement and verify their package, not return audit notes only, unless the task is explicitly read-only.
- Keep one writer per file at a time. The coordinator owns cross-cutting architecture decisions, shared API contracts, migrations, route registration, final integration, and TODO status. If packages need the same files or contract changes, sequence that work or assign one owner; do not ask concurrent agents to edit shared files and resolve collisions later.
- Use available concurrency slots efficiently (normally up to three subagents alongside the coordinator); run additional packages in waves. Recheck agent status and messages, continue unfinished work after failures, and do not treat a failed or abandoned subagent as completion.
- Each agent must read the current `AGENTS.md`, relevant TODO sections, and specifications before editing. Preserve all existing user changes. Never reset, clean, revert, overwrite, migrate, or delete user data or running Vitasil resources; tests must use uniquely named disposable databases and storage, after checking for active sessions and port conflicts.
- Require each agent to report changed paths, implemented acceptance criteria, exact commands and outcomes, and remaining gaps or environment limits. The coordinator reviews the shared diff, resolves integration issues, and runs final cross-package checks serially against one known disposable environment.
- Only the coordinator updates TODO checkboxes, and only after reviewing the implementation and evidence against the stated acceptance criteria. Record partial completion and failures accurately; do not mark the overall goal complete while required TODO items or safe verification remain.
- Give concise progress updates when a work package finishes, a dependency blocks another package, or a meaningful verification result changes. Do not pause the overall task merely because one package needs clarification when independent work can continue safely.

## Agreed target architecture

- Branches are defined statically in startup configuration, each with its own port and independent snapshot assignment, CORS, and indexing policy. Public routing belongs to the gateway; canonical domain is set in site settings. Do not create extra branches outside the configuration.
- Snapshots form a tree. Structural and design changes create a child snapshot; there are no persistent configuration drafts. Content drafts and published content are copied to children and become independent. Saving content creates a draft; explicit publication updates that snapshot and all branches assigned to it.
- Protect a snapshot assigned to a branch and its immediate parent from deletion. Reparent children when deleting an allowed intermediate snapshot; preserve their state. Never alter branch assignments without an explicit action.
- Feature ZIPs include `manifest.json`, which remains the source of feature metadata, site-editor `arguments`, typed `dataInputs`, form submission contracts, design tokens, and runtime metadata. Snapshot-scoped `dataModel` is the source of CMS entity/field schemas and record forms; do not put CMS editor layouts or record values in feature manifests. The graphical ER editor lives in `/site`; changes to model structure create child snapshots. New builds use the framework-neutral static runtime contracts in [docs/SPEC-feature-runtime.md](docs/SPEC-feature-runtime.md); preserve the existing feature runtime ABI unless a blocking defect requires a documented change.
- Preserve Vitasil feature UI, markup, and behavior when adapting to the Engine runtime. Do not wholesale rewrite Vitasil features to native DOM; adapt only the build/export boundary needed for an already-supported ABI, without introducing a new ABI value or changing feature contracts.
- The public runtime keeps `legacy-react-v1`, `static-module-v1`, and `static-html-v1`; do not migrate ABI or add an implicit manifest root-tag field for DOM cleanup. Each placement keeps one ABI-required neutral `<div>` lifecycle root. Avoid engine-added page wrappers and shared `background`/`header`/`footer` slot wrappers: non-main roots are direct children of `#root`; main roots are direct children of exactly one page-level `<main>`. `#root` carries runtime page metadata. Never nest placements. Feature-authored content remains semantically structured (`<article>`, `<section>`, lists, and forms as appropriate) without a nested `<main>`.
- The content and design editors show only features included in the selected snapshot, using their selected versions. Editorial content fields are optional, including nested fields; continue validating supplied values and preserve required-field validation for user-facing form submissions.
- Base design tokens and values belong to a snapshot; feature-specific tokens are declared by the feature manifest and shared by that feature's placements in the snapshot. The admin editor is graphical and must not require CSS, JSON, variable names, or expressions. Support graphical links to base tokens and configured screen-width overrides. Do not embed a live site preview in the design editor.
- Files have owners and public/private access policies. Service integrations use scoped service credentials; the `upload` permission includes choosing `public` or `private` visibility for each upload, and public files are intentionally anonymously readable. Preserve the agreed warning and confirmation before deleting used files, including the consequences for referenced content and snapshots; do not silently expose private files through storage or caches.
- Generate internal identifiers instead of requiring users to enter them. Configure operational values explicitly, with examples in example configs; do not introduce hidden runtime defaults. Protocol and algorithm constants may remain in code, and feature manifests may declare token defaults as part of their contract. Pass secrets via environment variables or secret files, never example credentials as real secrets.
- When implementing SEO or prerendering, use the agreed TODO, keep branch indexing policies and snapshot/content changes consistent with cached HTML, and verify real feature behavior and public HTML without claiming guaranteed search rankings.
- The model-v2 cutover uses a clean initial schema: the fresh baseline contains no retired per-feature CMS tables and no `ALTER` statements; defaults and constraints are declared with their tables. Compatibility/conversion for old dev/prod databases is not required. This does not authorize deleting existing databases, files, or user changes; rebuild only isolated disposable databases and refill them from current Vitasil v2 feature ZIPs during QA.

## Frontend conventions

- Use the existing React Router, TanStack Query, React Hook Form, Zod, Radix, Tailwind, and Lucide patterns.
- Use semantic HSL tokens from `frontend/ui/styles/admin.css` for admin surfaces; define matching light and dark values. Avoid new one-off colors and spacing values where a token or utility already fits.
- Keep request behavior in `api.ts`/domain hooks rather than duplicating fetch logic in pages. Handle loading, empty, error, and saving states where a workflow needs them.
- Keep React 18 compatibility. Do not apply React 19-only composition guidance.
- Make controls keyboard operable, label fields, provide visible focus, respect reduced motion, and keep mobile targets usable.

## Go HTML templates

- Keep HTML markup and `html/template` actions in embedded `.tmpl` files under the owning package's `templates/` directory. Go source builds typed view data, loads/parses the files, and executes named templates; do not embed template source in Go string constants or construct template actions by concatenating strings.
- Use contextual escaping for all dynamic values. Mark data as trusted HTML only when it is static markup from an application-owned embedded build/template and that trust boundary is documented; never pass user content as `template.HTML`, `template.JS`, or equivalent.
- Remove superseded inline template strings after migrating them, and update the relevant spec/QA evidence before closing its TODO item.

## Local checks

From `frontend/`, the supported scripts are `pnpm typecheck`, `pnpm lint`, `pnpm test`, and `pnpm build`. `go test ./...` and `go vet ./...` cover the backend. Browser QA should use the admin entry, confirm that its API target is this checkout before interacting, and record any environment limitation instead of treating it as an application pass. For changes affecting feature manifests, content, snapshots, tokens, or runtime, exercise applicable real Vitasil features in an isolated environment as well as focused automated tests.

## CodeGraph navigation

- CodeGraph CLI `@colbymchenry/codegraph@1.6.2` is installed globally under Node 20 and is the current npm `latest` version. To reproduce this npm install on this workstation, use `PATH='/Users/docup/.nvm/versions/node/v20.19.4/bin:/usr/bin:/bin:/opt/homebrew/bin' npm install --global @colbymchenry/codegraph@1.6.2`. The upstream standalone macOS/Linux installer is `curl -fsSL https://raw.githubusercontent.com/colbymchenry/codegraph/main/install.sh | sh` and does not need Node.
- `codegraph install` wires CodeGraph into agent MCP settings and may edit agent configuration. Both this checkout and Vitasil already have `.codex/config.toml` MCP entries, so do not rerun it just to refresh an index. Both repos already have `.codegraph/` indexes; run `codegraph init` only for a repository without an index, and use `codegraph status`/`codegraph sync` for existing ones.
- The `codegraph-usage` skill is installed at `~/.codex/skills/codegraph-usage/SKILL.md`; read it before substantial CodeGraph exploration. It comes from Badi21's community PR [#1117](https://github.com/colbymchenry/codegraph/pull/1117), which was closed without merging to upstream `main`. If the local skill is missing, install the pinned skill file with:

  ```sh
  mkdir -p "$HOME/.codex/skills/codegraph-usage"
  curl -fsSL https://raw.githubusercontent.com/Badi21/codegraph/0d52f9d6dfe5a4c5ba51571948b46602bd8f8e01/skills/codegraph-usage/SKILL.md \
    -o "$HOME/.codex/skills/codegraph-usage/SKILL.md"
  ```
- This checkout and `/Users/docup/Projects/vitasil` have local `.codegraph/` indexes. They are generated, machine-local data; keep them out of commits.
- For code structure, call `codegraph explore "<flow, symbol, or area>"`; use `codegraph node <symbol>` for one symbol and `codegraph callers`, `callees`, or `impact` to inspect dependencies before broad shared-code changes. Prefer one targeted exploration over repeated small calls. If the CodeGraph MCP tool is exposed by Codex, it can be used instead of the CLI.
- Check `codegraph status` in the relevant repository before relying on its graph. If files are pending or stale, read those current files directly or run `codegraph sync`; use `rg` for exact text and filenames, docs, configuration, and other unindexed content. CodeGraph is structural navigation, not semantic search, type checking, or test evidence.
- On this workstation, prepend Node 20 if the CodeGraph npm shim resolves to the broken Homebrew Node: `PATH="$HOME/.nvm/versions/node/v20.19.4/bin:$PATH" codegraph status` (or the relevant subcommand). The project MCP entry is in `.codex/config.toml`; if it is unavailable in the current Codex session, use the CLI.

## Project skills

- `admin-design-system`: admin visual language and token consistency.
- `admin-ux-architect`: route structure and CMS workflows.
- `admin-frontend-refactor`: React and TypeScript architecture.
- `admin-visual-qa`: responsive browser verification and screenshots.
- `admin-production-audit`: release checks and issue reporting.
