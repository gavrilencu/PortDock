package azuredevops

import "testing"

func TestInOrganization(t *testing.T) {
	cases := []struct {
		base, remote string
		want         bool
	}{
		{"https://dev.azure.com/contoso", "https://dev.azure.com/contoso/shop/_git/web", true},
		{"https://dev.azure.com/contoso", "https://dev.azure.com/Contoso/shop/_git/web", true},
		{"https://dev.azure.com/contoso", "https://contoso.visualstudio.com/shop/_git/web", true},
		{"https://contoso.visualstudio.com", "https://dev.azure.com/contoso/shop/_git/web", true},
		{"https://tfs.corp.local/tfs/Main", "https://tfs.corp.local/tfs/Main/shop/_git/web", true},
		{"https://dev.azure.com/contoso", "https://dev.azure.com/other/shop/_git/web", false},
		{"https://dev.azure.com/contoso", "https://evil.example.com/contoso/_git/web", false},
		{"https://dev.azure.com/contoso", "http://dev.azure.com/contoso/shop/_git/web", false},
		{"https://dev.azure.com/contoso", "https://dev.azure.com.evil.io/contoso/x", false},
		{"https://dev.azure.com/contoso", "https://contoso.visualstudio.com.evil.io/x", false},
		{"https://tfs.corp.local/tfs/Main", "https://tfs.corp.local/tfs/Other/x/_git/y", false},
		{"https://tfs.corp.local/tfs/Main", "https://tfs.corp.local.evil/tfs/Main/x", false},
	}
	for _, c := range cases {
		if got := InOrganization(c.base, c.remote); got != c.want {
			t.Errorf("InOrganization(%q, %q) = %v", c.base, c.remote, got)
		}
	}
}
