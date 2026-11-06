package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestFamilyOf(t *testing.T) {
	for release, want := range map[string]string{
		"ID=cachyos\nID_LIKE=arch":                              "arch",
		"ID=manjaro\nID_LIKE=arch":                              "arch",
		"ID=ubuntu\nID_LIKE=debian":                             "debian",
		"ID=linuxmint\nID_LIKE=\"ubuntu debian\"":               "debian",
		"ID=pop\nID_LIKE=\"ubuntu debian\"":                     "debian",
		"ID=fedora":                                             "fedora",
		"ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"":          "fedora",
		"ID=\"opensuse-tumbleweed\"\nID_LIKE=\"opensuse suse\"": "opensuse",
		"ID=gentoo": "other",
		"ID=nixos":  "other",
		"ID=void":   "other",
		"":          "other",
	} {
		if got := familyOf(strings.NewReader(release)); got != want {
			t.Errorf("%q: got %s, want %s", release, got, want)
		}
	}
}

func TestInstallSteps(t *testing.T) {
	avahi := dependency{ID: "avahi", Packages: map[string]string{"fedora": "avahi"}}
	base := dependency{ID: "base", Packages: map[string]string{"fedora": "gst-base"}}
	same := dependency{ID: "same", Packages: map[string]string{"fedora": "gst-base"}}
	manual := dependency{ID: "doubletake", Manual: true, Packages: map[string]string{"arch": "doubletake-bin"}}

	steps, left := installSteps("fedora", "", []dependency{avahi, base, same, manual})
	want := [][]string{{"sudo", "dnf", "install", "avahi", "gst-base"}, {"sudo", "systemctl", "enable", "--now", "avahi-daemon"}}
	if !reflect.DeepEqual(steps, want) || !reflect.DeepEqual(left, []string{"doubletake"}) {
		t.Errorf("fedora: %v, left %v", steps, left)
	}
	steps, left = installSteps("arch", "paru", []dependency{manual})
	if !reflect.DeepEqual(steps, [][]string{{"paru", "-S", "--needed", "doubletake-bin"}}) || left != nil {
		t.Errorf("arch: %v, left %v", steps, left)
	}
	if steps, left = installSteps("arch", "", []dependency{manual}); steps != nil || len(left) != 1 {
		t.Errorf("arch without an AUR helper: %v, left %v", steps, left)
	}
	if steps, left = installSteps("other", "", []dependency{avahi}); steps != nil || len(left) != 1 {
		t.Errorf("other: %v, left %v", steps, left)
	}
	if steps, left = installSteps("fedora", "", nil); steps != nil || left != nil {
		t.Errorf("nothing missing: %v, left %v", steps, left)
	}
}

func TestNoPackage(t *testing.T) {
	heif := dependency{ID: "libheif", Packages: map[string]string{"opensuse": "heif-dec"}, NoPackage: []string{"opensuse"}}
	if steps, left := installSteps("opensuse", "", []dependency{heif}); steps != nil || len(left) != 1 {
		t.Errorf("a package the repositories lack must not reach zypper: %v, left %v", steps, left)
	}
}
