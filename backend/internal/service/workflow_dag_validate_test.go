package service

import (
    "encoding/json"
    "testing"
)

func mkDef(nodes, edges string) *WorkflowDefinition {
    raw := `{"version":1,"nodes":` + nodes + `,"edges":` + edges + `}`
    var def WorkflowDefinition
    _ = json.Unmarshal([]byte(raw), &def)
    return &def
}

func TestValidateDefinition(t *testing.T) {
    ok := mkDef(
        `[{"id":"n1","type":"delay","config":{"seconds":1}},{"id":"n2","type":"http","config":{"url":"http://x","method":"GET"}}]`,
        `[{"id":"e1","source":"n1","target":"n2"}]`)
    if err := ValidateDefinition(ok); err != nil {
        t.Fatalf("valid dag rejected: %v", err)
    }

    cyc := mkDef(
        `[{"id":"n1","type":"delay","config":{"seconds":1}},{"id":"n2","type":"delay","config":{"seconds":1}}]`,
        `[{"id":"e1","source":"n1","target":"n2"},{"id":"e2","source":"n2","target":"n1"}]`)
    if err := ValidateDefinition(cyc); err == nil {
        t.Fatal("cycle not detected")
    }

    orphan := mkDef(
        `[{"id":"n1","type":"delay","config":{"seconds":1}},{"id":"n2","type":"delay","config":{"seconds":1}},{"id":"n3","type":"delay","config":{"seconds":1}}]`,
        `[{"id":"e1","source":"n1","target":"n2"}]`)
    if err := ValidateDefinition(orphan); err == nil {
        t.Fatal("orphan not detected")
    }

    badCond := mkDef(
        `[{"id":"c1","type":"condition","config":{"left":"$inputs.x","operator":"==","right":1}},{"id":"n2","type":"delay","config":{"seconds":1}}]`,
        `[{"id":"e1","source":"c1","target":"n2"}]`)
    if err := ValidateDefinition(badCond); err == nil {
        t.Fatal("condition edge without label not detected")
    }

    badRef := mkDef(
        `[{"id":"n1","type":"delay","config":{"seconds":1}},{"id":"n2","type":"delay","config":{"seconds":1}}]`,
        `[{"id":"e1","source":"n1","target":"nX"}]`)
    if err := ValidateDefinition(badRef); err == nil {
        t.Fatal("dangling edge not detected")
    }

    badType := mkDef(`[{"id":"n1","type":"loop","config":{}}]`, `[]`)
    if err := ValidateDefinition(badType); err == nil {
        t.Fatal("invalid node type not detected")
    }

    if err := ValidateDefinition(mkDef(`[]`, `[]`)); err == nil {
        t.Fatal("empty dag not rejected")
    }

    goodCond := mkDef(
        `[{"id":"c1","type":"condition","config":{"left":"$inputs.x","operator":">","right":1}},{"id":"nT","type":"delay","config":{"seconds":1}},{"id":"nF","type":"delay","config":{"seconds":1}},{"id":"nE","type":"delay","config":{"seconds":1}}]`,
        `[{"id":"e1","source":"c1","target":"nT","condition":"true"},{"id":"e2","source":"c1","target":"nF","condition":"false"},{"id":"e3","source":"nT","target":"nE"},{"id":"e4","source":"nF","target":"nE"}]`)
    if err := ValidateDefinition(goodCond); err != nil {
        t.Fatalf("valid condition dag rejected: %v", err)
    }
}