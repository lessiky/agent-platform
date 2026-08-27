package service

import (
	"encoding/json"
	"testing"

	"agent-platform/internal/model"

	"gorm.io/datatypes"
)

func TestRunPrintNodeResolvesVariables(t *testing.T) {
	e := &WorkflowEngine{}
	varCtx := &VarContext{
		Inputs:      map[string]interface{}{"order": "SO-1001"},
		NodeOutputs: map[string]interface{}{},
	}
	out, err := e.runPrintNode(map[string]interface{}{
		"message": "订单 $inputs.order 处理完成",
		"color":   "#1677ff",
	}, varCtx)
	if err != nil {
		t.Fatalf("runPrintNode: %v", err)
	}
	m, ok := out.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected output type: %T", out)
	}
	if m["message"] != "订单 SO-1001 处理完成" {
		t.Fatalf("message = %v", m["message"])
	}
	if m["color"] != "#1677ff" {
		t.Fatalf("color = %v", m["color"])
	}
}

func TestRunPrintNodeWholeReference(t *testing.T) {
	e := &WorkflowEngine{}
	varCtx := &VarContext{
		Inputs:      map[string]interface{}{},
		NodeOutputs: map[string]interface{}{"n1": map[string]interface{}{"text": "hello"}},
	}
	out, err := e.runPrintNode(map[string]interface{}{"message": "$nodes.n1.text"}, varCtx)
	if err != nil {
		t.Fatalf("runPrintNode: %v", err)
	}
	m := out.(map[string]interface{})
	if m["message"] != "hello" {
		t.Fatalf("message = %v", m["message"])
	}
	if m["color"] != "" {
		t.Fatalf("color = %v", m["color"])
	}
}

func TestRunPrintNodeEmptyAfterResolve(t *testing.T) {
	e := &WorkflowEngine{}
	varCtx := &VarContext{Inputs: map[string]interface{}{}, NodeOutputs: map[string]interface{}{}}
	if _, err := e.runPrintNode(map[string]interface{}{"message": "  "}, varCtx); err == nil {
		t.Fatal("empty message not rejected")
	}
}

func mkPrintStateMap(def *WorkflowDefinition) map[string]*nodeState {
	stateMap := make(map[string]*nodeState, len(def.Nodes))
	for i := range def.Nodes {
		stateMap[def.Nodes[i].ID] = &nodeState{def: &def.Nodes[i], record: &model.WorkflowNodeExecution{NodeID: def.Nodes[i].ID}}
	}
	return stateMap
}

func TestCollectPrintOutputSuccessOnlyInDefOrder(t *testing.T) {
	def := &WorkflowDefinition{
		Version: 1,
		Nodes: []WorkflowNodeDef{
			{ID: "n1", Type: model.NodeTypeDelay, Name: "延迟"},
			{ID: "n2", Type: model.NodeTypePrint, Name: "开始提示", Config: map[string]interface{}{"message": "开始", "color": "#f5222d"}},
			{ID: "n3", Type: model.NodeTypePrint, Name: "跳过节点", Config: map[string]interface{}{"message": "跳过"}},
			{ID: "n4", Type: model.NodeTypePrint, Name: "结束提示", Config: map[string]interface{}{"message": "结束"}},
		},
	}
	stateMap := mkPrintStateMap(def)
	stateMap["n2"].record.Status = model.NodeStatusSuccess
	stateMap["n2"].record.Output = datatypes.JSON(`{"message":"开始","color":"#f5222d"}`)
	stateMap["n3"].record.Status = model.NodeStatusSkipped
	stateMap["n4"].record.Status = model.NodeStatusSuccess
	stateMap["n4"].record.Output = datatypes.JSON(`{"message":"结束"}`)

	e := &WorkflowEngine{}
	payload := e.collectPrintOutput(def, stateMap)
	if payload == nil {
		t.Fatal("expected print output")
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(payload, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2: %v", len(entries), entries)
	}
	if entries[0]["node_name"] != "开始提示" || entries[0]["message"] != "开始" || entries[0]["color"] != "#f5222d" {
		t.Fatalf("entry0 = %v", entries[0])
	}
	if entries[1]["node_name"] != "结束提示" || entries[1]["message"] != "结束" {
		t.Fatalf("entry1 = %v", entries[1])
	}
}

func TestCollectPrintOutputNone(t *testing.T) {
	def := &WorkflowDefinition{
		Version: 1,
		Nodes: []WorkflowNodeDef{
			{ID: "n1", Type: model.NodeTypeDelay, Name: "延迟"},
		},
	}
	e := &WorkflowEngine{}
	if payload := e.collectPrintOutput(def, mkPrintStateMap(def)); payload != nil {
		t.Fatalf("expected nil, got %s", payload)
	}
}

func TestValidatePrintNodeConfig(t *testing.T) {
	okDef := mkDef(
		`[{"id":"n1","type":"print","name":"提示","config":{"message":"完成 $inputs.id","color":"#1677ff"}}]`,
		`[]`)
	if err := ValidateDefinition(okDef); err != nil {
		t.Fatalf("valid print node rejected: %v", err)
	}
	noMsg := mkDef(`[{"id":"n1","type":"print","config":{"color":"#1677ff"}}]`, `[]`)
	if err := ValidateDefinition(noMsg); err == nil {
		t.Fatal("print node without message accepted")
	}
	badColor := mkDef(`[{"id":"n1","type":"print","config":{"message":"x","color":"red"}}]`, `[]`)
	if err := ValidateDefinition(badColor); err == nil {
		t.Fatal("print node with invalid color accepted")
	}
}
