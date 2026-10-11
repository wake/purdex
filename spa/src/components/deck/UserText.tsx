// spa/src/components/deck/UserText.tsx — the text of a sent message, with each `[Image: source: <path>]` drawn as a thumbnail tag
// (v3 pt 5) in place, between the text before and after it. The path itself is not shown. The picture is read from the
// conversation's host (`POST /api/fs/read`, 10 MB at most); one that cannot be read is an icon and the file name. Only the
// person's own messages come here (UserBlock / UserBubble decide): a peer's or a task's text can name any path.
import { useContext } from 'react'
import { imageMarker, splitImageRefs } from '../../lib/conversations/image-placeholder'
import { baseName } from '../../lib/conversations/attachment-format'
import { isImagePath } from '../../lib/conversations/image-sniff'
import { useFsImage } from '../../lib/conversations/fs-image'
import { useInView } from './use-in-view'
import { AttachmentThumb } from './AttachmentThumb'
import { CjkText } from './CjkText'
import { ConversationHostContext } from './conversation-host'

function SentImageTag({ path }: { path: string }) {
  const hostId = useContext(ConversationHostContext)
  // read when it comes near the screen, not when the transcript mounts (a long one holds many tags far off screen)
  const [ref, seen] = useInView<HTMLSpanElement>()
  const image = useFsImage(hostId ?? '', path, hostId !== null && seen)
  return (
    <span ref={ref} data-testid="sent-image-tag" data-search-skip className="mx-0.5 inline-block align-middle">
      <AttachmentThumb name={baseName(path)} image src={image.status === 'ready' ? image.url : null} loading={image.status === 'loading' || (!seen && hostId !== null)} />
    </span>
  )
}

export function UserText({ text }: { text: string }) {
  const parts = splitImageRefs(text)
  if (!parts.some((p) => p.kind === 'image')) return <CjkText text={text} />
  // only a path that names an image file becomes a tag; any other stays the text it was (and is never read)
  return <>{parts.map((p, i) => (p.kind === 'text' ? <CjkText key={i} text={p.text} /> : isImagePath(p.path) ? <SentImageTag key={i} path={p.path} /> : <CjkText key={i} text={imageMarker(p.path)} />))}</>
}
