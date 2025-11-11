package session

// ResolveWorkingDir determines the correct directory to start claude --resume
//
// CRITICAL: Always returns projectPath, NOT lastCwd.
// Claude --resume only finds sessions stored in the project directory.
// The resume prompt tells Claude where the session last was (lastCwd).
//
// DO NOT CHANGE THIS - see commits db2bc33 and 33050ea
func ResolveWorkingDir(projectPath, lastCwd string) string {
	return projectPath
}
