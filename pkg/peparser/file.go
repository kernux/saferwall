package pe

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"sort"

	mmap "github.com/edsrzf/mmap-go"
)

// A File represents an open PE file.
type File struct {
	DosHeader        ImageDosHeader
	NtHeader         ImageNtHeader
	FileHeader       ImageFileHeader
	data mmap.MMap
}

// Open opens the named file using os.Open and prepares it for use as a PE binary.
func Open(name string) (File, error) {

	// Init an File instance
	file := File{}

	f, err := os.Open(name)
	if err != nil {
		return file, err
	}

	// Memory map the file insead of using read/write.
	data, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		f.Close()
		return file, err
	}

	file.data = data
	return file, nil
}



// Parse performs the file parsing for a PE binary.
func (pe *File) Parse() error {

	// Probes for the smallest PE size
	if len(pe.data) < TinyPESize {
		return ErrInvalidPESize
	}

	// Parse the DOS header
	err := pe.parseDosHeader()
	if err != nil {
		return err
	}

	// Parse the NT header
	err = pe.parseNtHeader()
	if err != nil {
		return err
	}

	// Parse the File Header
	err = pe.parseFileHeader()
	if err != nil {
		return err
	}

	// Parse the Optional Header
	err = pe.parseOptionalHeader()
	if err != nil {
		return err
	}

	return nil
}

func (pe *File) parseDosHeader() (err error) {
	offset := 0
	size := binary.Size(pe.DosHeader)
	buf := bytes.NewReader(pe.data[offset : offset+size])
	err = binary.Read(buf, binary.LittleEndian, &pe.DosHeader)
	if err != nil {
		return err
	}

	// it can be ZM on an (non-PE) EXE.
	// These executables still work under XP via ntvdm.
	if pe.DosHeader.Emagic != ImageDOSSignature && pe.DosHeader.Emagic != ImageDOSZMSignature {
		return ErrDOSMagicNotFound
	}

	// e_lfanew  is the only required element (besides the signature) of the DOS HEADER to turn the EXE into a PE.
	// is a relative offset to the NT Headers.
	// can't be null (signatures would overlap)
	// can be 4 at minimum
	if pe.DosHeader.Elfanew < 4 || pe.DosHeader.Elfanew > uint32(len(pe.data)) {
		return ErrInvalidElfanewValue
	}

	return nil
}


func (pe *File) parseNtHeader() (err error) {
	ntHeaderOffset := pe.DosHeader.Elfanew
	size := uint32(binary.Size(pe.NtHeader))
	buf := bytes.NewReader(pe.data[ntHeaderOffset : ntHeaderOffset+size])
	err = binary.Read(buf, binary.LittleEndian, &pe.NtHeader)
	if err != nil {
		return err
	}

	// Probe for PE signature.
	if pe.NtHeader.Signature == ImageOS2Signature {
		return ErrImageOS2SignatureFound
	}
	if pe.NtHeader.Signature == ImageOS2LESignature {
		return ErrImageOS2LESignatureFound
	}
	if pe.NtHeader.Signature == ImageVXDignature {
		return ErrImageVXDSignatureFound
	}
	if pe.NtHeader.Signature == ImageTESignature {
		return ErrImageTESignatureFound
	}

	// this is the smallest requirement for a valid PE
	if pe.NtHeader.Signature != ImageNTSignature {
		return ErrImageNtSignatureNotFound
	}

	return nil
}


func (pe *File) parseFileHeader() (err error) {
	fileHeaderOffset := pe.DosHeader.Elfanew + uint32(binary.Size(pe.NtHeader))
	size := uint32(binary.Size(pe.FileHeader))
	buf := bytes.NewReader(pe.data[fileHeaderOffset : fileHeaderOffset+size])
	err = binary.Read(buf, binary.LittleEndian, &pe.FileHeader)
	if err != nil {
		return err
	}

	return nil
}


func (pe *File) parseOptionalHeader() (err error) {

	fileHeaderOffset := pe.DosHeader.Elfanew + uint32(binary.Size(pe.NtHeader))
	optionalHeaderOffset := fileHeaderOffset + uint32(binary.Size(pe.FileHeader))

	// We read it as OptionHeader32 then we fix up later.
	size := uint32(binary.Size(pe.OptionalHeader))
	buf := bytes.NewReader(pe.data[optionalHeaderOffset : optionalHeaderOffset+size])
	err = binary.Read(buf, binary.LittleEndian, &pe.OptionalHeader)
	if err != nil {
		return err
	}

	// Probes for PE32/PE32+ optional header magic.
	if pe.OptionalHeader.Magic != ImageNtOptionalHeader32Magic && pe.OptionalHeader.Magic != ImageNtOptionalHeader64Magic {
		return ErrImageNtOptionalHeaderMagicNotFound
	}

	// Are we dealing with a PE64 optional header.
	if pe.OptionalHeader.Magic == ImageNtOptionalHeader64Magic {
		size = uint32(binary.Size(pe.OptionalHeader64))
		buf = bytes.NewReader(pe.data[optionalHeaderOffset : optionalHeaderOffset+size])
		err = binary.Read(buf, binary.LittleEndian, &pe.OptionalHeader64)
		if err != nil {
			return err
		}
		pe.Is64 = true
	}

	// ImageBase should be multiple of 10000h
	if pe.Is64 && pe.OptionalHeader64.ImageBase%0x10000 != 0 {
		return ErrImageBaseNotAligned
	}
	if !pe.Is64 && pe.OptionalHeader.ImageBase%0x10000 != 0 {
		return ErrImageBaseNotAligned
	}

	// ImageBase can be any value as long as ImageBase + 'SizeOfImage' < 80000000h for PE32
	if !pe.Is64 && pe.OptionalHeader.ImageBase+pe.OptionalHeader.SizeOfImage >= 0x80000000 {
		return ErrImageBaseOverflow
	}

	return nil
}