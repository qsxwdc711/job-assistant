package ingest

// mokaConfig binds each approved source to its observed public tenant contract.
// Brand URL slugs and organization IDs are independent, as with Ourpalm/playcrab.
func mokaConfig(source string) (mokaSite, bool) {
	const campusEndpoint = "/api/outer/ats-apply/website/jobs/v2"
	var site mokaSite
	switch source {
	case "nsfocus-campus":
		return mokaSite{source: source, company: "nsfocus", org: "nsfocus", site: "29118", mode: "campus", path: "/campus_apply/nsfocus/29118", endpoint: campusEndpoint}, true
	case "shokz-campus":
		return mokaSite{source: source, company: "shokz", org: "aftershokzhr", site: "36940", mode: "campus", path: "/campus-recruitment/aftershokzhr/36940", endpoint: campusEndpoint}, true
	case "bluepoch-careers":
		return mokaSite{source: source, company: "bluepoch", org: "blueinteractive", site: "38433", mode: "social", path: "/apply/blueinteractive/38433", endpoint: "/api/outer/ats-apply/website/jobs"}, true
	case "voyah-campus":
		site = mokaSite{company: "voyah", org: "voyah", site: "146293", path: "/campus-recruitment/voyah/146293"}
	case "weride-campus":
		site = mokaSite{company: "weride", org: "jingchi", site: "2137", path: "/campus_apply/jingchi/2137"}
	case "kingnet-campus":
		site = mokaSite{company: "kingnet", org: "kingnet", site: "2246", path: "/campus_apply/kingnet/2246"}
	case "ourpalm-campus":
		site = mokaSite{company: "ourpalm", org: "playcrab", site: "43628", path: "/campus-recruitment/ourpalm/43628"}
	case "ztgame-campus":
		site = mokaSite{company: "ztgame", org: "ztgame", site: "92438", path: "/campus-recruitment/ztgame/92438"}
	case "sungrow-campus":
		site = mokaSite{company: "sungrow", org: "sungrow", site: "94416", path: "/campus-recruitment/sungrow/94416"}
	case "37games-campus":
		site = mokaSite{company: "37games", org: "37", site: "58016", path: "/campus-recruitment/37/58016"}
	case "funplus-campus":
		site = mokaSite{company: "funplus", org: "funplus01", site: "147931", path: "/campus-recruitment/funplus01/147931"}
	case "bosch-campus":
		site = mokaSite{company: "bosch", org: "bosch", site: "168626", path: "/campus-recruitment/bosch/168626"}
	case "ey-campus":
		site = mokaSite{company: "ey", org: "ey", site: "166374", path: "/campus-recruitment/ey/166374"}
	case "cambricon-campus":
		site = mokaSite{company: "cambricon", org: "cambricon", site: "44201", path: "/campus-recruitment/cambricon/44201"}
	default:
		return mokaSite{}, false
	}
	site.source, site.mode, site.endpoint = source, "campus", campusEndpoint
	site.structuredQualifications = true
	return site, true
}
