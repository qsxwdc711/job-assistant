package ingest

import (
	"strconv"

	"jobassistant/backend/internal/catalog"
)

func tencentVariants(programme tencentProgramme, mappingID int, id string, detail map[string]any) ([]catalog.Variant, error) {
	if id == "-2" && mappingID == 104 && programme.recruitmentType == "internship" {
		return tencentProjectVariants(detail)
	}
	if text(detail["postId"]) != id || PlainText(text(detail["title"])) == "" || PlainText(text(detail["desc"])) == "" || PlainText(text(detail["request"])) == "" {
		return nil, ErrSchema
	}
	bonus := "graduateBonus"
	if programme.recruitmentType == "internship" {
		bonus = "internBonus"
	}
	variant := tencentVariant(detail, "main", bonus)
	if variant.Direction == "unknown" {
		return nil, nil
	}
	return []catalog.Variant{variant}, nil
}

// tencentProjectVariants handles the verified daily project pool (-2 / project 12).
// Its real child IDs identify directions; the parent explicitly provides common requirements.
func tencentProjectVariants(detail map[string]any) ([]catalog.Variant, error) {
	parentID, exists := detail["postId"]
	duties, requirements := PlainText(text(detail["desc"])), PlainText(text(detail["request"]))
	if !exists || parentID != nil || text(detail["projectId"]) != "12" || text(detail["recruitType"]) != "2" || text(detail["title"]) != "项目实习生-技术" || duties == "" || requirements == "" {
		return nil, ErrSchema
	}
	groups := array(detail["projectInternDirections"])
	if len(groups) == 0 {
		return nil, ErrSchema
	}
	var variants []catalog.Variant
	seen := map[string]bool{}
	for _, group := range groups {
		children := array(object(group)["subProjectInterns"])
		if len(children) == 0 {
			return nil, ErrSchema
		}
		for _, entry := range children {
			child := object(entry)
			id := text(child["postId"])
			numericID, err := strconv.ParseUint(id, 10, 64)
			if err != nil || numericID == 0 || seen[id] || text(child["projectId"]) != "12" || PlainText(text(child["title"])) == "" {
				return nil, ErrSchema
			}
			seen[id] = true
			variant := tencentVariant(child, id, "internBonus")
			if child["desc"] == nil && child["request"] == nil {
				variant.Responsibilities = "项目共同职责：\n" + duties
				variant.Requirements = "项目共同要求：\n" + requirements
			} else if variant.Responsibilities == "" || variant.Requirements == "" {
				return nil, ErrSchema
			}
			variant.Direction = Direction(variant.Name, variant.Responsibilities)
			variant.Major = ParseMajor(variant.Requirements)
			if variant.Direction != "unknown" {
				variants = append(variants, variant)
			}
		}
	}
	return variants, nil
}

func tencentVariant(detail map[string]any, key, bonus string) catalog.Variant {
	name := PlainText(text(detail["title"]))
	duties, requirements := PlainText(text(detail["desc"])), PlainText(text(detail["request"]))
	departments := []catalog.DepartmentGroup{}
	for _, bg := range array(detail["intentionBGDList"]) {
		for _, dept := range array(object(bg)["departmentList"]) {
			d := object(dept)
			departments = append(departments, catalog.DepartmentGroup{Code: text(d["id"]), Name: PlainText(text(d["name"])), Locations: Locations(joinedStrings(d["workCityList"], ""))})
		}
	}
	return catalog.Variant{Key: key, Name: name, Direction: Direction(name, duties), Responsibilities: duties, Requirements: requirements, Preferred: PlainText(text(detail[bonus])), Major: ParseMajor(requirements), Locations: Locations(joinedStrings(detail["workCityList"], "")), Departments: departments}
}
