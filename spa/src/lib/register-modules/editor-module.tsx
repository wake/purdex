import type { ModuleDefinition } from '../module-registry'
import type { PaneContent } from '../../types/tab'
import { registerFileOpener } from '../file-opener-registry'
import { EditorPane } from '../../components/editor/EditorPane'
import { EditorBuffersPane } from '../../components/editor/EditorBuffersPane'
import { ImagePreviewPane } from '../../components/editor/ImagePreviewPane'
import { PdfPreviewPane } from '../../components/editor/PdfPreviewPane'
import { EditorHomePathWorkspaceSection } from '../../components/editor/EditorHomePathWorkspaceSection'
import { EditorHomePathHostSection } from '../../components/editor/EditorHomePathHostSection'
import { EditorPurdexSettingsSection } from '../../components/settings/EditorPurdexSettingsSection'

const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico'])
const PDF_EXTS = new Set(['pdf'])
const BINARY_EXTS = new Set([...IMAGE_EXTS, ...PDF_EXTS])

export const editorModuleDefinition: ModuleDefinition = {
  id: 'editor',
  name: 'Editor',
  disableable: true,
  descriptionKey: 'modules.editor.description',
  panes: [
    { kind: 'editor', component: EditorPane },
    { kind: 'editor-buffers', component: EditorBuffersPane },
    { kind: 'image-preview', component: ImagePreviewPane },
    { kind: 'pdf-preview', component: PdfPreviewPane },
  ],
  settings: [
    {
      localId: 'editor',
      scope: 'purdex',
      order: 9,
      labelKey: 'settings.section.editor',
      component: EditorPurdexSettingsSection,
    },
    {
      localId: 'workspace-home-path',
      scope: 'workspace',
      order: 0,
      labelKey: 'editor.settings.home_path.workspace',
      component: EditorHomePathWorkspaceSection,
    },
    {
      localId: 'host-home-path',
      scope: 'host',
      order: 100,
      labelKey: 'editor.settings.home_path.host',
      component: EditorHomePathHostSection,
    },
  ],
}

// Inline registrations remain here until Task 1.3 promotes them into
// editorModuleDefinition.fileOpeners and applyModuleFileOpeners() takes over.
export function registerEditorFileOpeners(): void {
  registerFileOpener({
    id: 'image-preview',
    ownerModuleId: 'editor',
    label: 'Image Preview',
    icon: 'Image',
    match: (file) => IMAGE_EXTS.has(file.extension.toLowerCase()),
    priority: 'default',
    createContent: (source, file) =>
      ({ kind: 'image-preview', source, filePath: file.path }) as PaneContent,
  })

  registerFileOpener({
    id: 'pdf-viewer',
    ownerModuleId: 'editor',
    label: 'PDF Viewer',
    icon: 'FilePdf',
    match: (file) => PDF_EXTS.has(file.extension.toLowerCase()),
    priority: 'default',
    createContent: (source, file) =>
      ({ kind: 'pdf-preview', source, filePath: file.path }) as PaneContent,
  })

  registerFileOpener({
    id: 'monaco-editor',
    ownerModuleId: 'editor',
    label: 'Text Editor',
    icon: 'File',
    match: (file) =>
      !file.isDirectory && !BINARY_EXTS.has(file.extension.toLowerCase()),
    priority: 'default',
    createContent: (source, file) =>
      ({ kind: 'editor', source, filePath: file.path }) as PaneContent,
  })
}
