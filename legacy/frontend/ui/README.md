# Frontend UI layer

`@website-engine/ui` is a small workspace package consumed by the admin application. It exports CSS only; it does not contain application components or route definitions. `ui/styles` owns the admin style entrypoints:

- `foundation.css` provides the minimal reset used by the admin application.
- `admin.css` is the admin design system and Tailwind layer.

Admin React components remain under `admin/src/components`. Public feature runtime scripts live in `pkg/runtime/scripts`, and the isolated public stylesheet lives in `pkg/runtime/styles`; they are built as assets and served by the Go document handler. Admin CSS is never included in public branch assets. Snapshot design tokens continue to be resolved by the public runtime from site data.
