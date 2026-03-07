# Test Fixtures for Downloader

This directory contains sample files for testing media validation logic.

## Directory Structure

```
testdata/
├── valid/          # Files that should pass validation
└── invalid/        # Files that should fail validation
```

## Valid Files

### sample.mp4
- **Purpose**: Test MP4 file validation
- **Magic Bytes**: `00 00 00 00 ftyp` (ftyp at offset 4)
- **Size**: ~1KB
- **Expected**: Passes validation

### sample.webm
- **Purpose**: Test WebM file validation
- **Magic Bytes**: `1A 45 DF A3` (EBML header)
- **Size**: ~1KB
- **Expected**: Passes validation

### sample.jpg
- **Purpose**: Test JPEG file validation
- **Magic Bytes**: `FF D8 FF` (Start of Image marker)
- **Size**: ~1KB
- **Expected**: Passes validation

### sample.png
- **Purpose**: Test PNG file validation
- **Magic Bytes**: `89 50 4E 47` (PNG signature)
- **Size**: ~1KB
- **Expected**: Passes validation

### sample.gif
- **Purpose**: Test GIF file validation
- **Magic Bytes**: `47 49 46 38 39 61` (GIF89a header)
- **Size**: ~1KB
- **Expected**: Passes validation

## Invalid Files

### html_as_mp4.mp4
- **Purpose**: Test HTML content with .mp4 extension
- **Content**: `<!DOCTYPE html>...`
- **Expected**: Fails validation (wrong content type)

### empty.mp4
- **Purpose**: Test empty file handling
- **Size**: 0 bytes
- **Expected**: Fails validation (empty file)

### tiny.mp4
- **Purpose**: Test very small file handling
- **Size**: 17 bytes
- **Content**: "too small content"
- **Expected**: Fails validation (too small)

### png_as_mp4.mp4
- **Purpose**: Test wrong file format with .mp4 extension
- **Content**: PNG data (89 50 4E 47...)
- **Expected**: Fails validation (wrong format)

### error.html
- **Purpose**: Test error HTML page
- **Content**: 500 Internal Server Error HTML
- **Expected**: Fails validation (HTML, not media)

## Verification

To verify magic bytes:

```bash
# MP4
hexdump -C testdata/valid/sample.mp4 | head -1
# Expected: 00000000  00 00 00 00 66 74 79 70  ...

# WebM
hexdump -C testdata/valid/sample.webm | head -1
# Expected: 00000000  1a 45 df a3 ...

# JPEG
hexdump -C testdata/valid/sample.jpg | head -1
# Expected: 00000000  ff d8 ff ...

# PNG
hexdump -C testdata/valid/sample.png | head -1
# Expected: 00000000  89 50 4e 47 ...

# GIF
hexdump -C testdata/valid/sample.gif | head -1
# Expected: 00000000  47 49 46 38 39 61 ...
```

## File Format Specifications

- **MP4**: ISO base media file format, "ftyp" box at offset 4
- **WebM**: EBML header starts with 0x1A45DFA3
- **JPEG**: Start of Image marker 0xFFD8, followed by 0xFF
- **PNG**: PNG signature 0x89504E47
- **GIF**: Header "GIF89a" or "GIF87a"