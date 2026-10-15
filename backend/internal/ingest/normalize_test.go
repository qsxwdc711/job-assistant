package ingest

import (
	"testing"
)

func TestTextAndUnknownConditions(t *testing.T) {
	if got := PlainText("<p>计算机&nbsp;相关专业</p><script>bad()</script><br>每周5天"); got != "计算机 相关专业\n每周5天" {
		t.Fatalf("safe plain text: %q", got)
	}
	if m := ParseMajor("具备编程能力"); m.Kind != "unknown" || len(m.Tags) != 0 {
		t.Fatalf("undisclosed major: %#v", m)
	}
	if m := ParseMajor("计算机、通信等相关专业"); m.Kind != "related" || len(m.Tags) != 2 {
		t.Fatalf("related major: %#v", m)
	}
	if m := ParseMajor("专业不限"); m.Kind != "unrestricted" {
		t.Fatalf("unrestricted major: %#v", m)
	}
	if m := ParseMajor("计算机相关专业优先"); m.Kind != "preferred" {
		t.Fatalf("preference became a hard major restriction: %#v", m)
	}
	if m := ParseMajor("专业能力：\n专业软件知识，包括算法、软件工程、数据库"); m.Kind != "unknown" {
		t.Fatalf("skill heading became a major restriction: %#v", m)
	}
}

func TestLocationKeepsOriginalAndDoesNotInventCity(t *testing.T) {
	locations := Locations("深圳总部/合肥/未公布")
	if len(locations) != 3 || locations[0].City != "深圳市" || locations[0].Raw != "深圳总部" || locations[2].Kind != "unknown" {
		t.Fatalf("locations: %#v", locations)
	}
}

func TestTechnicalDirectionDoesNotUseMajorAsJobDuties(t *testing.T) {
	if Direction("市场营销助理", "客户维护与银行营销") != "unknown" {
		t.Fatal("nontechnical job admitted")
	}
	if Direction("后台开发", "网络和数据库") != "backend" {
		t.Fatal("backend classification lost")
	}
	if Direction("招商银行研究院实习生（宏观经济研究方向）", "宏观研究、数据分析和论文写作") != "unknown" {
		t.Fatal("macroeconomic research became a computing role")
	}
	if Direction("供应链管理", "供应链数据分析与供应商安全管理") != "unknown" {
		t.Fatal("business duties became a computing role")
	}
	for title, want := range map[string]string{"AI全栈工程师": "backend", "AI应用工程师": "algorithm_ai", "云服务开发工程师": "backend", "数据科学工程师": "data"} {
		if got := Direction(title, ""); got != want {
			t.Errorf("explicit computing role %s: %s", title, got)
		}
	}
}
