# Judged columns for the corpus-fitness sample

The judged half of the projection (the design merged in director#288, section 4 item 3), for the probe's 20 sampled entries only, joined to `projection.tsv` on R id. No parser writes these columns. They are the research team's reading on 2026-10-25, one reader, against director `62b55fc`; valid until 2026-11-08.

- **Admission** uses the ruled test, "would a working director fix this?", as `skills/director/SKILL.md` states it at the pin (lines 477 to 489; the design's 219 to 232 has drifted). Product mechanism and role contract both count as product.
- **Layer** applies to product rows only: transport (the message bus, where the shim and its tests exist) or application (not built). The practice row takes none.
- **Second reader** marks the rows the research team asked a second reader to check.

| id | admission | shape | layer | level | second reader |
|---|---|---|---|---|---|
| R-05 | product mechanism | requirement | transport | L1 |  |
| R-14 | product mechanism | requirement | transport | L2 |  |
| R-23 | product mechanism | requirement | application | L2 |  |
| R-32 | product mechanism | decision | application | L1 |  |
| R-41 | product mechanism | requirement | transport | L2 |  |
| R-50 | product mechanism | requirement | transport | L3 |  |
| R-59 | product mechanism | requirement | application | L2 |  |
| R-68 | practice | requirement | - | - | yes |
| R-77 | product mechanism | requirement | transport | L2 |  |
| R-86 | product mechanism | decision | transport | L1 |  |
| R-95 | product mechanism | requirement | transport | L3 |  |
| R-104 | product mechanism | requirement | transport | L2 |  |
| R-113 | product mechanism | requirement | application | L2 |  |
| R-122 | product mechanism | requirement | transport | L2 | yes |
| R-131 | role contract | requirement | application | L2 |  |
| R-140 | product mechanism | decision | application | L1 |  |
| R-149 | product mechanism | requirement | application | L3 |  |
| R-158 | product mechanism | requirement | transport | L2 |  |
| R-167 | product mechanism | decision | transport | L3 |  |
| R-176 | role contract | requirement | application | L2 | yes |

Totals: product mechanism 17, role contract 2, practice 1, environment fact 0 (product 19 of 20); requirement 16, decision 4; transport 11, application 8; L1 4, L2 11, L3 4, no level 1.
