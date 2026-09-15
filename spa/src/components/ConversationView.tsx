// spa/src/components/ConversationView.tsx
import { useRef, useCallback, useState } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'
import {
  type StreamMessage,
  type ControlRequest,
} from '../lib/stream-ws'
import { compositeKey } from '../lib/composite-key'
import ConversationMessages from './ConversationMessages'
import PermissionPrompt from './PermissionPrompt'
import AskUserQuestion from './AskUserQuestion'
import StreamInput from './StreamInput'
import FileAttachment, { type AttachedFile } from './FileAttachment'
import HandoffButton from './HandoffButton'

interface Props {
  hostId: string
  sessionCode: string
  isActive?: boolean
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}

const EMPTY_MESSAGES: StreamMessage[] = []
const EMPTY_CONTROLS: ControlRequest[] = []

export default function ConversationView({ hostId, sessionCode, isActive = false, onHandoff, onHandoffToTerm }: Props) {
  const t = useI18nStore((s) => s.t)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [attachedFiles, setAttachedFiles] = useState<AttachedFile[]>([])
  const [isDragging, setIsDragging] = useState(false)
  const dragCounter = useRef(0)
  const ck = compositeKey(hostId, sessionCode)

  // Read per-session state from store (keyed by composite key)
  const messages = useStreamStore((s) => s.sessions[ck]?.messages ?? EMPTY_MESSAGES)
  const pendingControlRequests = useStreamStore((s) => s.sessions[ck]?.pendingControlRequests ?? EMPTY_CONTROLS)
  const isStreaming = useStreamStore((s) => s.sessions[ck]?.isStreaming ?? false)
  const conn = useStreamStore((s) => s.sessions[ck]?.conn ?? null)
  const relayConnected = useStreamStore((s) => s.relayStatus[ck] ?? false)
  const handoffProgress = useStreamStore((s) => s.handoffProgress[ck] ?? '')
  const agentStatus = useAgentStore((s) => s.statuses[ck])

  // ThinkingIndicator: visible when streaming and no assistant messages yet
  const hasAssistantMessage = messages.some((m) => m.type === 'assistant')
  const showThinking = isStreaming && !hasAssistantMessage

  const handleSend = useCallback((text: string) => {
    conn?.send({
      type: 'user',
      message: { role: 'user', content: text },
    })
    useStreamStore.getState().addMessage(hostId, sessionCode, {
      type: 'user' as const,
      message: {
        role: 'user',
        content: [{ type: 'text', text }],
        stop_reason: null,
      },
    } as StreamMessage)
    useStreamStore.getState().setStreaming(hostId, sessionCode, true)
    setAttachedFiles([])
  }, [conn, hostId, sessionCode])

  const handleAllow = useCallback((req: ControlRequest) => {
    conn?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: req.request.input,
    })
    useStreamStore.getState().resolveControlRequest(hostId, sessionCode, req.request_id)
  }, [conn, hostId, sessionCode])

  const handleDeny = useCallback((req: ControlRequest) => {
    conn?.sendControlResponse(req.request_id, {
      behavior: 'deny',
      message: 'User denied',
    })
    useStreamStore.getState().resolveControlRequest(hostId, sessionCode, req.request_id)
  }, [conn, hostId, sessionCode])

  const handleAskAnswer = useCallback((req: ControlRequest, answer: string) => {
    const input = req.request.input as Record<string, unknown> | undefined
    const questions = (input?.questions as Array<Record<string, unknown>>) || []
    const questionText = questions.length > 0
      ? (questions[0].question as string) || ''
      : ''
    conn?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: {
        questions,
        answers: { [questionText]: answer },
      },
    })
    useStreamStore.getState().resolveControlRequest(hostId, sessionCode, req.request_id)
  }, [conn, hostId, sessionCode])

  const handleRemoveFile = useCallback((index: number) => {
    setAttachedFiles((prev) => prev.filter((_, i) => i !== index))
  }, [])

  // File attachment helpers
  function processFiles(files: FileList | File[]) {
    Array.from(files).forEach((file) => {
      if (file.type.startsWith('image/')) {
        const reader = new FileReader()
        reader.onload = (ev) => {
          setAttachedFiles((prev) => [...prev, {
            name: file.name,
            type: file.type,
            url: (ev.target?.result as string) || '',
          }])
        }
        reader.readAsDataURL(file)
      } else {
        setAttachedFiles((prev) => [...prev, { name: file.name, type: file.type, url: '' }])
      }
    })
  }

  function handleAttach() {
    fileInputRef.current?.click()
  }

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    if (e.target.files) {
      processFiles(e.target.files)
    }
    e.target.value = '' // reset for re-select
  }

  // Drag-drop handlers
  function handleDragEnter(e: React.DragEvent) {
    e.preventDefault()
    dragCounter.current++
    setIsDragging(true)
  }

  function handleDragOver(e: React.DragEvent) {
    e.preventDefault()
  }

  function handleDragLeave(e: React.DragEvent) {
    e.preventDefault()
    dragCounter.current--
    if (dragCounter.current <= 0) {
      setIsDragging(false)
      dragCounter.current = 0
    }
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setIsDragging(false)
    dragCounter.current = 0
    if (e.dataTransfer.files.length > 0) {
      processFiles(e.dataTransfer.files)
    }
  }

  const handleHandoff = useCallback(() => {
    if (onHandoff) {
      onHandoff()
    } else {
      useStreamStore.getState().setHandoffProgress(hostId, sessionCode, 'starting')
    }
  }, [onHandoff, hostId, sessionCode])

  // Show HandoffButton when relay is not connected (idle or handoff in progress)
  if (!relayConnected) {
    return (
      <div className="flex flex-col h-full">
        <HandoffButton
          inProgress={handoffProgress !== ''}
          progress={handoffProgress}
          agentStatus={agentStatus}
          onHandoff={handleHandoff}
        />
      </div>
    )
  }

  return (
    <div
      className="flex flex-col h-full relative"
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* Drag overlay */}
      {isDragging && (
        <div className="absolute inset-0 bg-blue-500/10 border-2 border-dashed border-blue-400 z-20 flex items-center justify-center pointer-events-none">
          <span className="text-blue-400 font-medium">{t('stream.drop_files')}</span>
        </div>
      )}

      {/* Hidden file input */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleFileChange}
      />

      {/* Messages area */}
      <ConversationMessages
        messages={messages}
        keyPrefix={sessionCode}
        showThinking={showThinking}
        showEmptyHint={messages.length === 0 && !isStreaming}
        scrollKey={pendingControlRequests.length}
        afterThinking={pendingControlRequests.map((req) => {
          if (req.request.tool_name === 'AskUserQuestion') {
            const input = req.request.input as Record<string, unknown> | undefined
            const questions = (input?.questions as Array<{
              question: string
              header?: string
              options?: Array<{ label: string; description?: string }>
              multiSelect?: boolean
            }>) || []
            return (
              <AskUserQuestion
                key={req.request_id}
                questions={questions}
                onSubmit={(answer) => handleAskAnswer(req, answer)}
                onCancel={() => handleDeny(req)}
              />
            )
          }
          const toolName = req.request.tool_name || 'Unknown'
          const description = req.request.input
            ? JSON.stringify(req.request.input).slice(0, 200)
            : 'Permission requested'
          return (
            <PermissionPrompt
              key={req.request_id}
              tool={toolName}
              description={description}
              onAllow={() => handleAllow(req)}
              onDeny={() => handleDeny(req)}
            />
          )
        })}
      />

      {/* File attachments */}
      <FileAttachment files={attachedFiles} onRemove={handleRemoveFile} />

      {/* Input area */}
      <StreamInput onSend={handleSend} onAttach={handleAttach} onHandoffToTerm={onHandoffToTerm} disabled={isStreaming} focused={isActive} />
    </div>
  )
}
