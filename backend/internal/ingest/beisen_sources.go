package ingest

type beisenSite struct {
	source, company, companyName, origin string
	tenantID                             int
}

func beisenConfig(source string) (beisenSite, bool) {
	switch source {
	case "miniso-campus":
		return beisenSite{source: source, company: "miniso", companyName: "名创优品", origin: "https://miniso.zhiye.com", tenantID: 109979}, true
	case "mxbc-campus":
		return beisenSite{source: source, company: "mxbc", companyName: "蜜雪冰城", origin: "https://careers.mxbc.com", tenantID: 111025}, true
	default:
		return beisenSite{}, false
	}
}
