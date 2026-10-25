package ingest

type workdayType struct {
	id, name, kind string
}

type workdaySite struct {
	source, company, host, tenant, site, china string
	types                                      []workdayType
}

func workdayConfig(source string) (workdaySite, bool) {
	if source != "nvidia-careers" {
		return workdaySite{}, false
	}
	return workdaySite{
		source: source, company: "nvidia", host: "https://nvidia.wd5.myworkdayjobs.com",
		tenant: "nvidia", site: "NVIDIAExternalCareerSite", china: "2fcb99c455831013ea529fe151e3323c",
		types: []workdayType{
			{id: "0c40f6bd1d8f10adf6dae42e46d44a17", name: "Intern (Fixed Term)", kind: "internship"},
			{id: "ab40a98049581037a3ada55b087049b7", name: "New College Graduate", kind: "campus"},
			{id: "0c40f6bd1d8f10adf6dae161b1844a15", name: "Regular Employee", kind: "early_career"},
		},
	}, true
}
