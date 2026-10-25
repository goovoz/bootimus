package ukitest

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"testing"
)

type Section struct {
	Name string
	Data []byte
}

func Write(t testing.TB, path string, sections ...Section) {
	t.Helper()

	const (
		peOffset   = 0x40
		fileAlign  = 0x200
		sectAlign  = 0x1000
		headerSize = 0x400
	)

	var buf bytes.Buffer
	dos := make([]byte, peOffset)
	copy(dos, "MZ")
	binary.LittleEndian.PutUint32(dos[0x3c:], peOffset)
	buf.Write(dos)
	buf.WriteString("PE\x00\x00")

	binary.Write(&buf, binary.LittleEndian, pe.FileHeader{
		Machine:              pe.IMAGE_FILE_MACHINE_AMD64,
		NumberOfSections:     uint16(len(sections)),
		SizeOfOptionalHeader: uint16(binary.Size(pe.OptionalHeader64{})),
		Characteristics:      pe.IMAGE_FILE_EXECUTABLE_IMAGE | pe.IMAGE_FILE_LARGE_ADDRESS_AWARE,
	})

	align := func(n, a uint32) uint32 { return (n + a - 1) / a * a }
	raw := uint32(headerSize)
	virt := uint32(sectAlign)
	var headers []pe.SectionHeader32
	for _, s := range sections {
		var name [8]uint8
		copy(name[:], s.Name)
		size := align(uint32(len(s.Data)), fileAlign)
		headers = append(headers, pe.SectionHeader32{
			Name:             name,
			VirtualSize:      uint32(len(s.Data)),
			VirtualAddress:   virt,
			SizeOfRawData:    size,
			PointerToRawData: raw,
			Characteristics:  pe.IMAGE_SCN_CNT_INITIALIZED_DATA | pe.IMAGE_SCN_MEM_READ,
		})
		raw += size
		virt += align(uint32(len(s.Data)), sectAlign)
	}

	binary.Write(&buf, binary.LittleEndian, pe.OptionalHeader64{
		Magic:               0x20b,
		ImageBase:           0x140000000,
		SectionAlignment:    sectAlign,
		FileAlignment:       fileAlign,
		SizeOfImage:         virt,
		SizeOfHeaders:       headerSize,
		Subsystem:           pe.IMAGE_SUBSYSTEM_EFI_APPLICATION,
		NumberOfRvaAndSizes: 16,
	})
	for _, h := range headers {
		binary.Write(&buf, binary.LittleEndian, h)
	}
	buf.Write(make([]byte, headerSize-buf.Len()))
	for i, s := range sections {
		buf.Write(s.Data)
		buf.Write(make([]byte, int(headers[i].SizeOfRawData)-len(s.Data)))
	}

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func WriteUKI(t testing.TB, path, cmdline string) {
	t.Helper()
	sections := []Section{{Name: ".linux", Data: []byte("kernel")}}
	if cmdline != "" {
		sections = append(sections, Section{Name: ".cmdline", Data: []byte(cmdline + "\x00")})
	}
	Write(t, path, sections...)
}
