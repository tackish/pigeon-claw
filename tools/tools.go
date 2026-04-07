package tools

import "github.com/tackish/pigeon-claw/provider"

func Definitions() []provider.Tool {
	return []provider.Tool{
		{
			Name:        "shell_exec",
			Description: "Execute a shell command on the host machine. Returns stdout and stderr combined. Use this for any system operation.",
			Parameters: []provider.ToolParameter{
				{Name: "command", Type: "string", Description: "The shell command to execute", Required: true},
			},
		},
		{
			Name:        "read_file",
			Description: "Read the contents of a file at the given path.",
			Parameters: []provider.ToolParameter{
				{Name: "path", Type: "string", Description: "Absolute or relative file path to read", Required: true},
			},
		},
		{
			Name:        "write_file",
			Description: "Write content to a file at the given path. Creates the file if it doesn't exist, overwrites if it does.",
			Parameters: []provider.ToolParameter{
				{Name: "path", Type: "string", Description: "Absolute or relative file path to write", Required: true},
				{Name: "content", Type: "string", Description: "Content to write to the file", Required: true},
			},
		},
		{
			Name:        "screenshot",
			Description: "Capture a screenshot of the current screen. Returns the image which will be sent to the Discord channel.",
			Parameters:  []provider.ToolParameter{},
		},
		{
			Name:        "list_dir",
			Description: "List contents of a directory with file sizes and permissions.",
			Parameters: []provider.ToolParameter{
				{Name: "path", Type: "string", Description: "Directory path to list", Required: true},
			},
		},
		{
			Name:        "osascript",
			Description: "Execute an AppleScript command. Use this to control macOS applications, UI automation, and system features.",
			Parameters: []provider.ToolParameter{
				{Name: "script", Type: "string", Description: "The AppleScript code to execute", Required: true},
			},
		},
	}
}
