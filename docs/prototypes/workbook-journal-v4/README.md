# Workbook journal v4 — clickable layout prototype (throwaway)

Spec: `docs/specs/2026-10-14-workbook-journal-spec.md` (v4, §11). Reuses the real app shell (`TitleBar`, `TabBar`, `TeamPanel`, stores) and adds the v4 cards: journal (pinned band, filters, stream, 待確認 with a detail panel opening to the left), roadmap tab (under the team card), tasks tab (with hover linking).

Fixture: written fresh from the interface line's 2026-10-14 events (`prototype/fixture.ts`); no real data. The solo state has its own small fixture (Haiku todos as tasks).

Where the files go: `prototype/` -> `spa/prototypes/workbook-journal-v4/`, `components/` -> `spa/src/components/journal-v4-proto/`.

Run (from `spa/`): `npx vite -c prototypes/workbook-journal-v4/vite.config.ts --host <addr> --port 5183`.

The bottom-left 原型工具列 toggles team/solo, roadmap, tasks, a narrow window (840 px), simulates a new event, and simulates Haiku ticking the next open 待確認 item. `shots/` has six reference screenshots.
