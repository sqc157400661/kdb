package version

import "fmt"

type info struct {
	Version      string `json:"Version"`
	ShortVersion string `json:"ShortVersion"`
	BuildDate    string `json:"BuildDate"`
	Describe     string `json:"Describe"`
}

var CurrentVersion = info{
	Version:      "v0.0.1",
	ShortVersion: "0.1",
	BuildDate:    "2023.5.23",
	Describe:     "开发测试版本",
}

// PrintVersionInfo show version info to Stdout
func PrintVersionInfo() {
	fmt.Printf("Agent Version: %#v\n", CurrentVersion)
}
