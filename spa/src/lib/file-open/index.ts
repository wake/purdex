export {
  createOpenFileService,
  FileNotFoundError,
  isNotFoundError,
  type OpenFileContext,
  type OpenFileService,
  type OpenFileDeps,
  type PopupSpec,
  type PopupController,
  type TabOpener,
  type FsBackendForOpen,
} from './open-file'
export {
  showFileNotFoundPopup,
  hideFileNotFoundPopup,
  disposeForTests as __disposeFileNotFoundPopupForTests,
  type ShowCallbacks as FileNotFoundPopupCallbacks,
} from './file-not-found-popup-service'
export {
  fsSearchByCapability,
  FsSearchError,
  type SearchMatch,
  type SearchResponse,
  type SearchRootCapability,
  type SearchLimits,
} from './fs-search'
