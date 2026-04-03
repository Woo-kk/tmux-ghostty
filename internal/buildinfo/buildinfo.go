package buildinfo

type Info struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildDate   string `json:"build_date"`
	ReleaseRepo string `json:"release_repo"`
	PackageID   string `json:"package_id"`
}

var (
	Version     = "dev"
	Commit      = "unknown"
	BuildDate   = "unknown"
	ReleaseRepo = "Woo-kk/tumx-ghostty"
	PackageID   = "com.guyuanshun.tmux-ghostty"
)

func Current() Info {
	return Info{
		Version:     Version,
		Commit:      Commit,
		BuildDate:   BuildDate,
		ReleaseRepo: ReleaseRepo,
		PackageID:   PackageID,
	}
}
