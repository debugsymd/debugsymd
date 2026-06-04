package symstore

import (
	"testing"

	"github.com/debugsymd/debugsymd/resolver"
)

func TestParse(t *testing.T) {
	const (
		pdbSig  = "0C1033F78632492E91C6C314B72E1920ffffffff"
		pdbUUID = "0c1033f7-8632-492e-91c6-c314b72e1920-ffffffff"
		// 16-byte build-id 01..10 -> little-endian-swapped debug ID.
		buildID  = "0102030405060708090a0b0c0d0e0f10aabbccdd"
		buildDbg = "04030201-0605-0807-090a-0b0c0d0e0f10"
		machUUID = "67e9247c814e392ba027dbde6748fcbf"
		machDash = "67e9247c-814e-392b-a027-dbde6748fcbf"
	)

	cases := []struct {
		name      string
		leading   string
		signature string
		trailing  string
		ok        bool
		want      resolver.Request
		ct        string
		cab       bool
	}{
		{
			name: "pdb", leading: "a.pdb", signature: pdbSig, trailing: "a.pdb", ok: true,
			want: resolver.Request{DebugID: pdbUUID, Filename: "a.pdb", FileType: resolver.FilePDB},
			ct:   OctetStream,
		},
		{
			name: "pdb compressed", leading: "a.pd_", signature: pdbSig, trailing: "a.pd_", ok: true,
			want: resolver.Request{DebugID: pdbUUID, Filename: "a.pdb", FileType: resolver.FilePDB},
			ct:   CABContentType, cab: true,
		},
		{
			name: "pe catch-all", leading: "a.dll", signature: "5E9C4F2A12000", trailing: "a.dll", ok: true,
			want: resolver.Request{CodeID: "5E9C4F2A12000", Filename: "a.dll", FileType: resolver.FilePE},
			ct:   OctetStream,
		},
		{
			name: "pe compressed", leading: "a.dl_", signature: "5E9C4F2A12000", trailing: "a.dl_", ok: true,
			want: resolver.Request{CodeID: "5E9C4F2A12000", Filename: "a.dll", FileType: resolver.FilePE},
			ct:   CABContentType, cab: true,
		},
		{
			name: "elf code", leading: "libc.so", signature: "elf-buildid-" + buildID, trailing: "libc.so", ok: true,
			want: resolver.Request{CodeID: buildID, DebugID: buildDbg, Filename: "libc.so", FileType: resolver.FileELFCode},
			ct:   OctetStream,
		},
		{
			name: "elf debug", leading: "_.debug", signature: "elf-buildid-sym-" + buildID, trailing: "_.debug", ok: true,
			want: resolver.Request{CodeID: buildID, DebugID: buildDbg, Filename: "_.debug", FileType: resolver.FileELFDebug},
			ct:   OctetStream,
		},
		{
			name: "mach code", leading: "app", signature: "mach-uuid-" + machUUID, trailing: "app", ok: true,
			want: resolver.Request{CodeID: machUUID, DebugID: machDash, Filename: "app", FileType: resolver.FileMachCode},
			ct:   OctetStream,
		},
		{
			name: "mach debug", leading: "_.dwarf", signature: "mach-uuid-sym-" + machUUID, trailing: "_.dwarf", ok: true,
			want: resolver.Request{CodeID: machUUID, DebugID: machDash, Filename: "_.dwarf", FileType: resolver.FileMachDebug},
			ct:   OctetStream,
		},
		{
			name: "elf source bundle", leading: "libc.src.zip", signature: "elf-buildid-" + buildID, trailing: "libc.src.zip", ok: true,
			want: resolver.Request{CodeID: buildID, DebugID: buildDbg, Filename: "libc.src.zip", FileType: resolver.FileSourceBundle},
			ct:   ZipContentType,
		},
		{
			name: "pdb source bundle", leading: "a.src.zip", signature: pdbSig, trailing: "a.src.zip", ok: true,
			want: resolver.Request{DebugID: pdbUUID, Filename: "a.src.zip", FileType: resolver.FileSourceBundle},
			ct:   ZipContentType,
		},
		{name: "empty signature", leading: "a.bin", signature: "", trailing: "a.bin", ok: false},
		{name: "bad elf buildid", leading: "x", signature: "elf-buildid-zzzz", trailing: "x", ok: false},
		{name: "bad mach uuid", leading: "x", signature: "mach-uuid-deadbeef", trailing: "x", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, serve, ok := Parse(tc.leading, tc.signature, tc.trailing)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}

			if !tc.ok {
				return
			}

			if req != tc.want {
				t.Fatalf("request = %+v, want %+v", req, tc.want)
			}

			if serve.ContentType != tc.ct {
				t.Fatalf("content-type = %q, want %q", serve.ContentType, tc.ct)
			}

			if serve.CAB != tc.cab {
				t.Fatalf("cab = %v, want %v", serve.CAB, tc.cab)
			}
		})
	}
}
