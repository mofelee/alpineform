<p align="right"><strong>English</strong> | <a href="localization-policy.zh.md">简体中文</a></p>

# Documentation Localization Policy

Every maintained AlpineForm Markdown document must be available in English and Simplified
Chinese. `AGENTS.md` is the only exception because it is repository-operational metadata,
not product, user, or maintainer documentation.

## File Naming

- In the repository root, English uses `<name>.md` and Simplified Chinese uses
  `<name>.zh-CN.md`.
- Outside the repository root, English uses `<name>.md` and Simplified Chinese uses
  `<name>.zh.md`.
- Both files in a pair stay in the same directory so relative assets and neighboring
  documentation use the same base path.

## Translation Requirements

- A translation covers the complete source document; summaries and placeholder-only
  counterparts are not acceptable.
- Commands, CLI output, HCL, code, URLs, hashes, versions, compatibility tiers, identifiers,
  paths, and verification evidence retain their exact technical meaning.
- Heading levels, fenced-code blocks and languages, tables, and checklists retain equivalent
  structure across the pair.
- Product behavior, operational procedures, support boundaries, and security guarantees remain
  identical in both languages.
- Shared behavior, support status, release history, or policy changes update both languages in
  the same change.

## Navigation

- Every document has a prominent reciprocal language selector near the top and marks its current
  language.
- English documents link to English documentation by default. Simplified Chinese documents link
  to Simplified Chinese documentation by default.
- The language selector is the only routine cross-language documentation link.
- Repository-local links use relative paths and must resolve, including heading fragments.

## Distribution

GoReleaser archives, the curl installer, and `make install` include the bilingual root documents
and complete bilingual `docs/` tree. Distribution tests derive their expected Markdown inventory
from the maintained source tree so a newly added document cannot be omitted silently.

## Validation

Run the documentation gate before committing:

```bash
make docs-check
```

The gate verifies pair coverage and naming, selectors, structural and technical parity,
translation coverage, English prose, same-language navigation, local files and fragments, and
release/install declarations. `make check` and hosted CI run the same gate.
