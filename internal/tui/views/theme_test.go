package views

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/blairham/tuikit/theme"

	"github.com/blairham/dockmaster/internal/tui/style"
)

// TestNoPackageStyleSkipsTheSkin: a style or color kept at package level
// in the views or the app must be derived in applyTheme, not in its
// declaration — a declaration runs once, before any skin is loaded, and
// would keep the default colors for good.
func TestNoPackageStyleSkipsTheSkin(t *testing.T) {
	for _, dir := range []string{".", ".."} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		if len(files) == 0 {
			t.Fatalf("no Go files in %s — the scan saw nothing", dir)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			af, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range af.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec) //nolint:forcetypeassert // a var decl holds ValueSpecs
					for _, v := range vs.Values {
						if themed(v) {
							t.Errorf("%s: %s is styled in its declaration; set it in applyTheme",
								fset.Position(vs.Pos()), vs.Names[0].Name)
						}
					}
				}
			}
		}
	}
}

// themed reports whether e reads the palette: lipgloss, pkgTheme, or a
// style color or style (style.View* constants are not colors).
func themed(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			found = found || n.Name == "lipgloss" || n.Name == "pkgTheme"
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && x.Name == "style" && !strings.HasPrefix(n.Sel.Name, "View") {
				found = true
			}
		}
		return true
	})
	return found
}

// TestSetBaseReachesTheViews: a new base theme recolors the palette and
// the views' own package-level styles.
func TestSetBaseReachesTheViews(t *testing.T) {
	t.Cleanup(func() { style.SetBase(theme.Default()) })
	skinned := theme.Default()
	skinned.Status.Error = lipgloss.Color("#123456")
	skinned.Accent = lipgloss.Color("#abcdef")
	style.SetBase(skinned)
	if style.ColorRed != skinned.Status.Error || style.Error.GetForeground() != skinned.Status.Error {
		t.Error("the palette kept the old error color")
	}
	if eventTypeStyle.GetForeground() != skinned.Accent || pkgTheme.Accent != skinned.Accent {
		t.Error("the views kept the old theme")
	}
}
