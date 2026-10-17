package gate

// CleanGitEnv is env without the variables that tell git which repository, work tree, index and object store to use (a process
// started by a git hook carries them). The wwd submission runs its git calls with it, as the snapshot does.
func CleanGitEnv(env []string) []string { return gitEnvWithoutRepository(env) }
