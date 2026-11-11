# Project Guidelines

- Keep technical design documents in `specs/`, separated by document type.
- Do not duplicate end-user documentation there; usage documentation belongs with the code.
- Keep documents concise and update them when the design changes.
- Enforce a maximum line width of 120 columns on prose; Markdown tables or links may exceed it.
- `README.md` is a landing page only: one-line pitch, minimal quickstart, and pointers to `specs/` for design
  details. Do not enumerate supported agents or duplicate spec content.
- `specs/` holds architecture, module boundaries, and verification strategy. Remove implementation details
  already expressed in code, and update `specs/architecture.md` when boundaries change.
- Schema changes travel together. Any change to `trium.schema.yaml` also updates `trium.example.yaml`, the
  relevant spec (e.g., `specs/interfaces.md` for manifest contracts or `specs/architecture.md` for design
  impact), `README.md` if user-facing, and tests.
