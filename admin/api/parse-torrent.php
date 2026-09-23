<?php
/**
 * Parse .torrent file and extract info hash
 *
 * This endpoint accepts a .torrent file upload and returns:
 * - Info hash (SHA-1)
 * - Torrent name
 * - File size
 * - Piece count
 */

require_once __DIR__ . '/../config.php';

header('Content-Type: application/json');

// This endpoint previously had NO authentication of any kind: it never loaded
// config.php, so anyone on the internet could POST arbitrary bytes into the
// bencode parser below. It is an admin tool and is now gated accordingly.
if (!isAuthenticated()) {
    http_response_code(401);
    echo json_encode(['error' => 'Authentication required']);
    exit;
}

// Parsing an upload is a state-changing operation from the browser's point of
// view, so it carries the same token as every <form> post.
csrf_require(true);

if ($_SERVER['REQUEST_METHOD'] !== 'POST') {
    http_response_code(405);
    echo json_encode(['error' => 'Method not allowed']);
    exit;
}

if (!isset($_FILES['torrent']) || $_FILES['torrent']['error'] !== UPLOAD_ERR_OK) {
    http_response_code(400);
    echo json_encode(['error' => 'No torrent file uploaded']);
    exit;
}

// Bound the input before it reaches the parser. A .torrent metafile is
// kilobytes; anything far larger is not a torrent and should not be decoded.
const MAX_TORRENT_BYTES = 4 * 1024 * 1024;

if (($_FILES['torrent']['size'] ?? 0) > MAX_TORRENT_BYTES) {
    http_response_code(413);
    echo json_encode(['error' => 'Torrent file too large']);
    exit;
}

$torrentFile = $_FILES['torrent']['tmp_name'];

// Reject anything that is not an actual uploaded file, so a crafted request
// cannot point this at a path on the server.
if (!is_uploaded_file($torrentFile)) {
    http_response_code(400);
    echo json_encode(['error' => 'Invalid upload']);
    exit;
}

$torrentData = file_get_contents($torrentFile);

if ($torrentData === false) {
    http_response_code(400);
    echo json_encode(['error' => 'Failed to read torrent file']);
    exit;
}

/**
 * Simple bencode decoder.
 *
 * $depth bounds recursion. Bencode nests without limit by design, so a short
 * input of the form "llllll..." would otherwise recurse until the process
 * exhausts its stack and dies. The bound turns that into a clean 400.
 */
const BDECODE_MAX_DEPTH = 32;

function bdecode($str, &$pos = 0, $depth = 0) {
    if ($depth > BDECODE_MAX_DEPTH) {
        throw new Exception('Invalid bencode: nesting too deep');
    }

    if ($pos >= strlen($str)) {
        return null;
    }

    $char = $str[$pos];

    // Integer: i<number>e
    if ($char === 'i') {
        $pos++;
        $end = strpos($str, 'e', $pos);
        if ($end === false) {
            throw new Exception('Invalid bencode: unterminated integer');
        }
        $num = substr($str, $pos, $end - $pos);
        $pos = $end + 1;
        return (int)$num;
    }

    // String: <length>:<string>
    if (is_numeric($char)) {
        $colon = strpos($str, ':', $pos);
        if ($colon === false) {
            throw new Exception('Invalid bencode: no colon in string');
        }
        $len = (int)substr($str, $pos, $colon - $pos);
        if ($len < 0 || $colon + 1 + $len > strlen($str)) {
            throw new Exception('Invalid bencode: string length out of range');
        }
        $pos = $colon + 1;
        $string = substr($str, $pos, $len);
        $pos += $len;
        return $string;
    }

    // List: l<items>e
    if ($char === 'l') {
        $pos++;
        $list = [];
        while ($pos < strlen($str) && $str[$pos] !== 'e') {
            $list[] = bdecode($str, $pos, $depth + 1);
        }
        $pos++; // skip 'e'
        return $list;
    }

    // Dictionary: d<key><value>...e
    if ($char === 'd') {
        $pos++;
        $dict = [];
        while ($pos < strlen($str) && $str[$pos] !== 'e') {
            $key = bdecode($str, $pos, $depth + 1);
            $value = bdecode($str, $pos, $depth + 1);
            $dict[$key] = $value;
        }
        $pos++; // skip 'e'
        return $dict;
    }

    throw new Exception('Invalid bencode: unexpected character');
}

/**
 * Bencode encoder (for info dict)
 */
function bencode($data) {
    if (is_int($data)) {
        return 'i' . $data . 'e';
    }

    if (is_string($data)) {
        return strlen($data) . ':' . $data;
    }

    if (is_array($data)) {
        // Check if associative array (dictionary)
        if (array_keys($data) !== range(0, count($data) - 1)) {
            // Dictionary
            ksort($data); // Keys must be sorted
            $encoded = 'd';
            foreach ($data as $key => $value) {
                $encoded .= bencode($key) . bencode($value);
            }
            $encoded .= 'e';
            return $encoded;
        } else {
            // List
            $encoded = 'l';
            foreach ($data as $value) {
                $encoded .= bencode($value);
            }
            $encoded .= 'e';
            return $encoded;
        }
    }

    throw new Exception('Cannot bencode type: ' . gettype($data));
}

try {
    // Decode torrent file
    $torrent = bdecode($torrentData);

    if (!isset($torrent['info'])) {
        throw new Exception('Invalid torrent: missing info dictionary');
    }

    // Extract info dictionary and re-encode it (must be exact for hash)
    $infoDictStart = strpos($torrentData, '4:info') + 6; // Position after '4:info'

    // Find the info dictionary end by manual parsing
    $pos = $infoDictStart;
    bdecode($torrentData, $pos); // This advances $pos to the end of info dict
    $infoDictEnd = $pos;

    $infoDict = substr($torrentData, $infoDictStart, $infoDictEnd - $infoDictStart);

    // Calculate SHA-1 hash of info dictionary
    $infoHash = sha1($infoDict);

    // Extract torrent metadata
    $name = isset($torrent['info']['name']) ? $torrent['info']['name'] : 'Unknown';

    // Calculate total size
    $totalSize = 0;
    if (isset($torrent['info']['length'])) {
        // Single file torrent
        $totalSize = $torrent['info']['length'];
        $fileCount = 1;
    } elseif (isset($torrent['info']['files'])) {
        // Multi-file torrent
        $fileCount = count($torrent['info']['files']);
        foreach ($torrent['info']['files'] as $file) {
            $totalSize += $file['length'];
        }
    }

    $pieceLength = isset($torrent['info']['piece length']) ? $torrent['info']['piece length'] : 0;
    $pieceCount = $pieceLength > 0 ? ceil($totalSize / $pieceLength) : 0;

    // Extract announce URL
    $announce = isset($torrent['announce']) ? $torrent['announce'] : null;

    // Return metadata
    echo json_encode([
        'success' => true,
        'info_hash' => $infoHash,
        'name' => $name,
        'size' => $totalSize,
        'size_formatted' => formatBytes($totalSize),
        'files' => $fileCount,
        'pieces' => $pieceCount,
        'announce' => $announce,
        'created_by' => isset($torrent['created by']) ? $torrent['created by'] : null,
        'creation_date' => isset($torrent['creation date']) ? $torrent['creation date'] : null,
    ]);

} catch (Exception $e) {
    http_response_code(400);
    echo json_encode([
        'error' => 'Failed to parse torrent: ' . $e->getMessage()
    ]);
}

// formatBytes() is provided by config.php, which this file now loads.
// Re-declaring it here would be a fatal error.
