package tmux

import "context"

// ReadWaitDelay exposes readWaitDelay so the deadline tests can bound how long
// a killed read may take to return.
const ReadWaitDelay = readWaitDelay

// ActivePaneMetadataFormat exposes the combined display-message format so a
// test can pin which fields go through tmux's TAB substitution.
const ActivePaneMetadataFormat = activePaneMetadataFormat

// ActivePaneMetadataPerField exposes the per-field fallback read so tests can
// assert the combined read is equivalent to it.
func (r *RealExecutor) ActivePaneMetadataPerField(ctx context.Context, sessionName string) (TmuxPaneMetadata, error) {
	return r.activePaneMetadataPerField(ctx, sessionName)
}
