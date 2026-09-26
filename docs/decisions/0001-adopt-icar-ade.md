# 1. Adopt ICAR ADE 1.5.1 resources and paths

**Status:** accepted

**Context.** Livestock data systems exchange data through ICAR Animal Data
Exchange. Using its published resources, rather than look-alike schemas, lets
any ADE-aware client or herd-management system integrate without a mapping
layer. It also makes the domain model reviewable against a public standard.

**Decision.** Vendor the ICAR 1.5.1 bundle and use its resources, identifier
schemes, metric codes and location-scoped paths as published. Put
Agritelem-specific data in clearly marked, additive extensions. See
[icar-alignment.md](../icar-alignment.md).

**Consequences.**
- Generated code in both languages represents ICAR's `allOf` inheritance,
  which is verbose in Swift. The mapping is confined to
  `APIClient/Mapping.swift`.
- Agritelem-specific needs (derived statistics, remark categories, herd
  KPIs) must be expressed as extensions and documented as such.
