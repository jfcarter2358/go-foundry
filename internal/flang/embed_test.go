package flang

import "testing"

func TestParseAndEvalAnExpressionInAChildEnv(t *testing.T) {
	prog := NewProgram()
	in, errs := NewInterp(prog, nil, Limits{})
	if len(errs) > 0 {
		t.Fatalf("interp: %v", errs)
	}
	e, perrs := ParseExpr("n1", []byte("amount * 2 + 1"))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	env := in.NewChildEnv()
	env.Declare("amount", IntVal(20))
	v, err := in.Eval(e, env)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "41" {
		t.Fatalf("got %v", v)
	}
}

func TestABlankExpressionIsNull(t *testing.T) {
	in, _ := NewInterp(NewProgram(), nil, Limits{})
	e, errs := ParseExpr("n1", []byte("   "))
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	v, err := in.Eval(e, nil)
	if err != nil || v.Type() != TypeNull {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestTrailingTextIsReported(t *testing.T) {
	if _, errs := ParseExpr("n1", []byte("1 + 2 end")); len(errs) == 0 {
		t.Fatal("wanted an error")
	}
}
