package cab

// On-disk CAB layout constants. See the Microsoft Cabinet File Format reference:
// https://learn.microsoft.com/en-us/previous-versions/bb417343(v=msdn.10)
const (
	// headerSignature is the CFHEADER magic ("MSCF").
	headerSignature = "MSCF"
	// versionMajor / versionMinor are the only version CAB readers accept (1.3).
	versionMajor = 1
	versionMinor = 3

	// compressionMSZIP is the CFFOLDER typeCompress value selecting MSZIP.
	compressionMSZIP = 0x0001

	// attribArchive marks the file with the DOS "archive" attribute (_A_ARCH).
	attribArchive = 0x20

	// Fixed structure sizes (no optional/reserved fields, flags == 0).
	cfHeaderSize    = 36
	cfFolderSize    = 8
	cfFileFixedSize = 16
	cfDataHeaderLen = 8

	// storedHeaderLen is the deflate stored-block header: BFINAL/BTYPE byte + LEN(2) + NLEN(2).
	storedHeaderLen = 5
	// deflateStoredFinalByte starts a deflate stored block: BFINAL=1, BTYPE=00, rest zero.
	deflateStoredFinalByte = 0x01

	// Byte offsets we back-patch after streaming: cbCabinet (total size) in the
	// header, cCFData (block count) in the single CFFOLDER.
	offCbCabinet = 8
	offCCFData   = cfHeaderSize + 4

	// maxBlockUncompressed is the MSZIP per-block window: 32 KiB.
	maxBlockUncompressed = 32768

	// maxBlocks is CFFOLDER.cCFData's u16 ceiling; with the 32 KiB window this caps
	// one folder at ~2 GiB. Larger inputs need a multi-folder cabinet (not synthesized).
	maxBlocks            = 0xFFFF
	maxFolderUncompessed = int64(maxBlocks) * maxBlockUncompressed

	// Fixed DOS date/time (1980-01-01 00:00:00) so a payload synthesizes
	// byte-identical output for reproducible caching and golden tests.
	dosDate = 0x0021
	dosTime = 0x0000
)

// mszipSignature ("CK") prefixes the deflate stream inside every CFDATA block.
var mszipSignature = [2]byte{'C', 'K'}
