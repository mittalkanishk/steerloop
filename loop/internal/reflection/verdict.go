package reflection

import "github.com/cloudwego/eino/schema"

const (
	VerdictPass = "pass"
	VerdictFail = "fail"
)

type Verdict struct {
	Verdict    string  `json:"verdict"`
	Reason     string  `json:"reason"`
	Hint       string  `json:"hint"`
	Confidence float64 `json:"confidence"`
}

func (v Verdict) Failed() bool {
	return v.Verdict == VerdictFail
}

const VerdictTool = "review_verdict"

var verdictInfo = &schema.ToolInfo{
	Name: VerdictTool,
	Desc: "Give your ruling on the work under review.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"verdict":    {Type: schema.String, Enum: []string{VerdictPass, VerdictFail}, Desc: "pass or fail", Required: true},
		"reason":     {Type: schema.String, Desc: "the issue or the drift; empty on pass", Required: true},
		"hint":       {Type: schema.String, Desc: "the next action or the fix; empty on pass"},
		"confidence": {Type: schema.Number, Desc: "0 to 1", Required: true},
	}),
}
