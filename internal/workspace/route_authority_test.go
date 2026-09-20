package workspace

import (
	"reflect"
	"testing"
)

func TestParseRouteAuthorityAndCandidates(t *testing.T) {
	content := `
# Routes
https://servocylmotion.com/products/
https://servocylmotion.com/products/electric-cylinders/
/capabilities/linear-actuator-oem-odm/
External: https://example.com/not-ours/
`
	authority := ParseRouteAuthority(content, "servocylmotion.com")
	for _, route := range []string{"/products", "/products/electric-cylinders", "/capabilities/linear-actuator-oem-odm"} {
		if !authority.Allows(route) {
			t.Fatalf("route %s was not parsed", route)
		}
	}
	if authority.Allows("/invented-route") {
		t.Fatal("invented route was allowed")
	}
	if !authority.Allows("https://example.com/not-ours/") {
		t.Fatal("external URL should be left to the domain policy")
	}

	got := RouteCandidates(map[string]any{
		"arguments": map[string]any{
			"page_url":  "https://servocylmotion.com/products/",
			"slug":      "electric-cylinders",
			"content":   "<a href=\"/not-scanned\">text</a>",
			"file_path": `D:\website\ServoCylMotion\a.html`,
		},
	})
	want := []string{"electric-cylinders", "https://servocylmotion.com/products/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates=%#v want=%#v", got, want)
	}
}

func TestRouteAuthorityPathResolvesProjectRelativeFile(t *testing.T) {
	item := policyWorkspace()
	item.RouteAuthority = "website_link.md"
	got, ok := RouteAuthorityPath(item, "windows")
	if !ok || got != "d:/website/servocylmotion/website_link.md" {
		t.Fatalf("path=%q ok=%v", got, ok)
	}
	item.RouteAuthority = "../ActuLift/website_link.md"
	if _, ok := RouteAuthorityPath(item, "windows"); ok {
		t.Fatal("parent traversal route authority was accepted")
	}
	item.RouteAuthority = `D:\website\ServoCylMotion\website_link.md`
	if got, ok := RouteAuthorityPath(item, "windows"); !ok || got != "d:/website/servocylmotion/website_link.md" {
		t.Fatalf("absolute in-root path=%q ok=%v", got, ok)
	}
	item.RouteAuthority = `D:\website\ActuLift\website_link.md`
	if _, ok := RouteAuthorityPath(item, "windows"); ok {
		t.Fatal("absolute route authority outside workspace roots was accepted")
	}
	item.ContextRoots = append(item.ContextRoots, `D:\shared\authority`)
	item.RouteAuthority = `D:\shared\authority\website_link.md`
	if got, ok := RouteAuthorityPath(item, "windows"); !ok || got != "d:/shared/authority/website_link.md" {
		t.Fatalf("absolute context-root path=%q ok=%v", got, ok)
	}
}
