// spa/src/components/room/attachment-source.ts — where a transcript's
// replayed image attachments are fetched from (phase E). ExecutionView
// provides it; AttachmentThumbs reads it. Null outside an execution pane,
// and every thumbnail is then the unavailable placeholder.
import { createContext } from 'react'

export const AttachmentSourceContext = createContext<{ hostId: string; executionId: string } | null>(null)
