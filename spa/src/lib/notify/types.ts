// spa/src/lib/notify/types.ts — the wire of the push-title group names (notify title spec §3.2): the app tells each daemon which group name stands for which lineage root.
export const NOTIFY_GROUP_LABELS_CAPABILITY = 'notify.group_labels.v1'
export const NOTIFY_GROUP_LABELS_PATH = '/api/notify/group-labels'
/** The daemon refuses a bigger set whole (413 `too_many_labels`); the app does not send one, and does not cut it. */
export const NOTIFY_GROUP_LABELS_MAX = 2000

export interface GroupLabel { root_session_id: string; group: string }
