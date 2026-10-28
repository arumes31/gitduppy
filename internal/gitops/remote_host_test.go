package gitops

import "testing"

func TestRemoteHost(t *testing.T) {
	cases := map[string]string{
		"https://github.com/user/repo.git":         "github.com",
		"https://user:tok@git.example.com/a/b.git": "git.example.com",
		"http://169.254.169.254/x.git":             "169.254.169.254",
		"ssh://git@host.example:2222/a/b.git":      "host.example",
		"git://[::1]/a.git":                        "::1",
		"git@github.com:user/repo.git":             "github.com",
		"git@127.0.0.1:user/repo.git":              "127.0.0.1",
		"no-host-here":                             "",
	}
	for in, want := range cases {
		if got := remoteHost(in); got != want {
			t.Errorf("remoteHost(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLFSConfigArgs(t *testing.T) {
	cases := map[string]string{
		"https://github.com/user/repo.git":         "lfs.url=https://github.com/user/repo.git/info/lfs",
		"https://github.com/user/repo":             "lfs.url=https://github.com/user/repo.git/info/lfs",
		"https://user:secret@git.example/a/b.git/": "lfs.url=https://git.example/a/b.git/info/lfs",
	}
	for in, want := range cases {
		args := lfsConfigArgs(in)
		if len(args) != 2 || args[0] != "-c" || args[1] != want {
			t.Errorf("lfsConfigArgs(%q)=%v want [-c %s]", in, args, want)
		}
	}
	for _, in := range []string{"git@github.com:user/repo.git", "ssh://git@host/a.git", ""} {
		if args := lfsConfigArgs(in); args != nil {
			t.Errorf("lfsConfigArgs(%q)=%v want nil", in, args)
		}
	}
}
