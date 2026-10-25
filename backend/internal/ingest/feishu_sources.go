package ingest

type feishuSite struct {
	source, company, origin, path, tenant string
	portal                                int
}

// feishuConfig contains individually authorized public career sites only.
func feishuConfig(source string) (feishuSite, bool) {
	switch source {
	case "bambulab-careers":
		return feishuSite{source: source, company: "bambulab", origin: "https://bambulab.jobs.feishu.cn", path: "campus", tenant: "Bambulab", portal: 6}, true
	case "agibot-campus":
		return feishuSite{source: source, company: "agibot", origin: "https://agirobot.jobs.feishu.cn", path: "campusrecruitment", tenant: "智元创新", portal: 6}, true
	case "insta360-campus":
		return feishuSite{source: source, company: "insta360", origin: "https://arashivision.jobs.feishu.cn", path: "campus", tenant: "影石创新", portal: 6}, true
	}
	return feishuSite{}, false
}
