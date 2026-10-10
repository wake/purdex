// spa/src/components/deck/conversation-host.ts — which host the conversation on screen lives on. The deck and the chat provide it;
// a message's image tags read their pictures from that host (never from another one). No provider: nothing can be read.
import { createContext } from 'react'

export const ConversationHostContext = createContext<string | null>(null)
