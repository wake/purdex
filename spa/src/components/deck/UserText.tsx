// spa/src/components/deck/UserText.tsx — the text of a sent message, with each `[Image: source: <path>]` drawn as a thumbnail tag
// (v3 pt 5) in place, between the text before and after it. The path itself is not shown. The picture is read from the
// conversation's host (`POST /api/fs/read`, 10 MB at most); one that cannot be read is an icon and the file name.
import { useContext } from 'react'
import { splitImageRefs } from '../../lib/conversations/image-placeholder'
import { baseName } from '../../lib/conversations/attachment-format'
import { useFsImage } from '../../lib/conversations/fs-image'
import { AttachmentThumb } from './AttachmentThumb'
import { ConversationHostContext } from './conversation-host'

function SentImageTag({ path }: { path: string }) {
  const hostId = useContext(ConversationHostContext)
  const image = useFsImage(hostId ?? '', path, hostId !== null)
  return (
    <span data-testid="sent-image-tag" className="mx-0.5 inline-block align-middle">
      <AttachmentThumb name={baseName(path)} image src={image.status === 'ready' ? image.url : null} loading={image.status === 'loading'} />
    </span>
  )
}

export function UserText({ text }: { text: string }) {
  const parts = splitImageRefs(text)
  if (!parts.some((p) => p.kind === 'image')) return <>{text}</>
  return <>{parts.map((p, i) => (p.kind === 'text' ? p.text : <SentImageTag key={i} path={p.path} />))}</>
}
