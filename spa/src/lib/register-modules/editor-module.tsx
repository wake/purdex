import type { ModuleDefinition } from '../module-registry'
import type { PaneContent } from '../../types/tab'
import { SETTINGS_ORDER } from '../settings-order'
import {
  registerNewTabProvider,
  unregisterNewTabProvidersByModule,
} from '../new-tab-registry'
import { EditorPane } from '../../components/editor/EditorPane'
import { StoragePane } from '../../components/editor/storage/StoragePane'
import { ImagePreviewPane } from '../../components/editor/ImagePreviewPane'
import { PdfPreviewPane } from '../../components/editor/PdfPreviewPane'
import { EditorNewTabSection } from '../../components/editor/EditorNewTabSection'
import { ManageBuffersNewTabCard } from '../../components/editor/ManageBuffersNewTabCard'
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
    { kind: 'editor-buffers', component: StoragePane },
    { kind: 'image-preview', component: ImagePreviewPane },
    { kind: 'pdf-preview', component: PdfPreviewPane },
  ],
  settings: [
    // Spec §4.2 (PR-2): the previously separate `link-detect` /
    // `open-behavior` purdex contributions were collapsed into inline
    // segments within `EditorPurdexSettingsSection`. The single canonical
    // purdex entry is `editor`; legacy URLs continue to resolve via
    // `URL_ALIASES` in `SettingsPage.tsx`.
    {
      localId: 'editor',
      scope: 'purdex',
      order: SETTINGS_ORDER.MODULE_EDITOR,
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
  fileOpeners: [
    {
      id: 'image-preview',
      label: 'Image Preview',
      icon: 'Image',
      match: (file) => IMAGE_EXTS.has(file.extension.toLowerCase()),
      priority: 'default',
      createContent: (source, file) =>
        ({ kind: 'image-preview', source, filePath: file.path }) as PaneContent,
    },
    {
      id: 'pdf-viewer',
      label: 'PDF Viewer',
      icon: 'FilePdf',
      match: (file) => PDF_EXTS.has(file.extension.toLowerCase()),
      priority: 'default',
      createContent: (source, file) =>
        ({ kind: 'pdf-preview', source, filePath: file.path }) as PaneContent,
    },
    {
      id: 'monaco-editor',
      label: 'Text Editor',
      icon: 'File',
      match: (file) =>
        !file.isDirectory && !BINARY_EXTS.has(file.extension.toLowerCase()),
      priority: 'default',
      createContent: (source, file) =>
        ({ kind: 'editor', source, filePath: file.path }) as PaneContent,
    },
  ],
}

/**
 * Register Editor's two new-tab providers (Files panel and Manage Buffers
 * card). Kept beside `editorModuleDefinition` so all editor-owned bootstrap
 * lives in one file; `register-modules/index.tsx` only orchestrates.
 *
 * Each provider carries `moduleId: 'editor'` so the New-Tab UI hides them
 * when the module is disabled, matching the pane / file-opener side. The
 * helper drops any pre-existing editor-owned entries before re-registering
 * so HMR / re-bootstrap stays idempotent.
 */
export function registerEditorNewTabProviders(): void {
  unregisterNewTabProvidersByModule('editor')

  registerNewTabProvider({
    id: 'editor',
    label: 'editor.provider_label',
    icon: 'File',
    order: 5,
    component: EditorNewTabSection,
    moduleId: 'editor',
  })

  registerNewTabProvider({
    id: 'editor-buffers',
    label: 'newTab.editor.buffers.label',
    icon: 'Stack',
    order: 6,
    component: ManageBuffersNewTabCard,
    moduleId: 'editor',
  })
}
