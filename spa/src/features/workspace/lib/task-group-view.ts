// spa/src/features/workspace/lib/task-group-view.ts — the gate in front of the sidebar's task-group view (name pool v2 spec §7, §9.6; NA-5 → NA-6). The view toggle (HomeRow) and the team-icon
// row of the profile editor are built, but a toggle with no task-group view behind it would be a button that does nothing, so they stay hidden until NA-6 ships the view. A code constant,
// not a setting and not in a store; NA-6 deletes this file. A plain object so a test can open it.
export const taskGroupView = { enabled: false }
