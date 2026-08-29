package service

import (
	"testing"
)

// jsonFuncTestCtx 构造 json() 函数解析的测试上下文
func jsonFuncTestCtx() *VarContext {
	return &VarContext{
		Inputs: map[string]interface{}{
			"raw": `{"order":{"total":199,"paid":true}}`,
		},
		NodeOutputs: map[string]interface{}{
			"n1": map[string]interface{}{
				"text": `{"data":{"id":42,"name":"widget"},"items":[{"sku":"a-1"},{"sku":"b-2"}],"ok":true,"score":3.5}`,
			},
			"n2": map[string]interface{}{"body": map[string]interface{}{"list": []interface{}{"x", "y"}}},
			"n4": map[string]interface{}{"text": "not a json"},
			"n5": map[string]interface{}{"text": "hello"},
		},
		ExecutionID: "exec1",
	}
}

func TestJsonFuncWholeRef(t *testing.T) {
	ctx := jsonFuncTestCtx()
	if got := ResolveVariables(`json($nodes.n1.text).data.name`, ctx); got != "widget" {
		t.Fatalf("string field: got %v", got)
	}
	if got := ResolveVariables(`json($nodes.n1.text).data.id`, ctx); got != float64(42) {
		t.Fatalf("number keeps type: got %T %v", got, got)
	}
	if got := ResolveVariables(`json($nodes.n1.text).ok`, ctx); got != true {
		t.Fatalf("bool: got %v", got)
	}
	if got := ResolveVariables(`json($nodes.n1.text).items[1].sku`, ctx); got != "b-2" {
		t.Fatalf("array index: got %v", got)
	}
	if got := ResolveVariables(`json($inputs.raw).order.total`, ctx); got != float64(199) {
		t.Fatalf("inputs ref: got %v", got)
	}
}

func TestJsonFuncWholeRefReturnsObject(t *testing.T) {
	ctx := jsonFuncTestCtx()
	got, ok := ResolveVariables(`json($nodes.n1.text)`, ctx).(map[string]interface{})
	if !ok {
		t.Fatalf("whole json object: got %T", got)
	}
	if _, hasData := got["data"]; !hasData {
		t.Fatalf("data key missing: %v", got)
	}
}

func TestJsonFuncEmbedded(t *testing.T) {
	ctx := jsonFuncTestCtx()
	if got := ResolveVariables(`订单: json($nodes.n1.text).data.name 完成`, ctx); got != "订单: widget 完成" {
		t.Fatalf("embedded: got %q", got)
	}
	if got := ResolveVariables(`id=json($nodes.n1.text).data.id`, ctx); got != "id=42" {
		t.Fatalf("embedded number formatting: got %q", got)
	}
	// 中文紧跟函数路径 (无空格)
	if got := ResolveVariables(`分析json($nodes.n1.text).data.name异常`, ctx); got != "分析widget异常" {
		t.Fatalf("cjk adjacent: got %q", got)
	}
	// 路径后为用户自定义包裹符号
	if got := ResolveVariables(`值=json($nodes.n1.text).data.name)结束`, ctx); got != "值=widget)结束" {
		t.Fatalf("paren wrapped: got %q", got)
	}
}

func TestJsonFuncNested(t *testing.T) {
	ctx := jsonFuncTestCtx()
	ctx.NodeOutputs["n3"] = map[string]interface{}{"text": `{"inner":"{\"deep\":\"v\"}"}`}
	if got := ResolveVariables(`json(json($nodes.n3.text).inner).deep`, ctx); got != "v" {
		t.Fatalf("nested json: got %v", got)
	}
}

func TestJsonFuncSpacesInCall(t *testing.T) {
	ctx := jsonFuncTestCtx()
	if got := ResolveVariables(`json( $nodes.n1.text ).data.name`, ctx); got != "widget" {
		t.Fatalf("spaces in call: got %v", got)
	}
}

func TestJsonFuncAlreadyObject(t *testing.T) {
	ctx := jsonFuncTestCtx()
	if got := ResolveVariables(`json($nodes.n2.body).list[1]`, ctx); got != "y" {
		t.Fatalf("already object: got %v", got)
	}
}

func TestJsonFuncInvalidJSON(t *testing.T) {
	ctx := jsonFuncTestCtx()
	// 解析失败: 函数整体未命中, 回退解析内层引用, 其余按字面量保留
	if got := ResolveVariables(`json($nodes.n4.text).data`, ctx); got != "json(not a json).data" {
		t.Fatalf("invalid json: got %q", got)
	}
}

func TestJsonFuncUnknownFunc(t *testing.T) {
	ctx := jsonFuncTestCtx()
	if got := ResolveVariables(`nope($nodes.n5.text).x`, ctx); got != "nope(hello).x" {
		t.Fatalf("unknown func: got %q", got)
	}
}

func TestJsonFuncMissingKeyKeepsText(t *testing.T) {
	ctx := jsonFuncTestCtx()
	// JSON 有效但路径不存在: 函数未命中, 回退解析内层引用
	want := "json(" + `{"data":{"id":42,"name":"widget"},"items":[{"sku":"a-1"},{"sku":"b-2"}],"ok":true,"score":3.5}` + ").nope"
	if got := ResolveVariables(`json($nodes.n1.text).nope`, ctx); got != want {
		t.Fatalf("missing key: got %q want %q", got, want)
	}
}
