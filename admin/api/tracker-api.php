<?php
/**
 * Tracker API Client
 *
 * Communicates with the Go tracker's /update endpoint
 * Implements all update actions: add/update/delete torrents, users, tokens, whitelist
 */

class TrackerAPI {
    private static $trackerUrl = 'http://localhost:34000';
    private static $sitePassword = 'changeme'; // Must match tracker config

    /**
     * Send a request to the tracker's update endpoint
     *
     * @param string $action The action to perform
     * @param array $data Additional data for the action
     * @return array Response from tracker
     * @throws Exception on error
     */
    private static function sendRequest($action, $data = []) {
        $data['action'] = $action;
        $jsonData = json_encode($data);

        $url = self::$trackerUrl . '/' . self::$sitePassword . '/update';

        $ch = curl_init($url);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_POST, true);
        curl_setopt($ch, CURLOPT_POSTFIELDS, $jsonData);
        curl_setopt($ch, CURLOPT_HTTPHEADER, [
            'Content-Type: application/json',
            'Content-Length: ' . strlen($jsonData)
        ]);
        curl_setopt($ch, CURLOPT_TIMEOUT, 5);

        $response = curl_exec($ch);
        $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);

        if (curl_errno($ch)) {
            $error = curl_error($ch);
            curl_close($ch);
            throw new Exception('Tracker connection error: ' . $error);
        }

        curl_close($ch);

        if ($httpCode !== 200) {
            throw new Exception('Tracker returned HTTP ' . $httpCode);
        }

        // Parse JSON response
        $result = json_decode($response, true);
        if ($result === null) {
            throw new Exception('Invalid JSON response from tracker');
        }

        if (!$result['success']) {
            throw new Exception($result['error'] ?? 'Unknown error');
        }

        return $result;
    }

    /**
     * Add a new torrent to the tracker
     *
     * @param int $torrentID The torrent ID
     * @param string $infoHash The info hash (20 or 40 chars)
     * @return array Response from tracker
     */
    public static function addTorrent($torrentID, $infoHash) {
        return self::sendRequest('add_torrent', [
            'torrent_id' => (int)$torrentID,
            'info_hash' => $infoHash
        ]);
    }

    /**
     * Delete a torrent from the tracker
     *
     * @param string $infoHash The info hash
     * @return array Response from tracker
     */
    public static function deleteTorrent($infoHash) {
        return self::sendRequest('delete_torrent', [
            'info_hash' => $infoHash
        ]);
    }

    /**
     * Change a torrent's freeleech status
     *
     * @param string $infoHash The info hash
     * @param int $freeType 0=normal, 1=free, 2=neutral
     * @return array Response from tracker
     */
    public static function changeFreeleech($infoHash, $freeType) {
        return self::sendRequest('change_freeleech', [
            'info_hash' => $infoHash,
            'free_type' => (int)$freeType
        ]);
    }

    /**
     * Add a new user to the tracker
     *
     * @param int $userID The user ID
     * @param string $passkey The 32-character passkey
     * @param bool $canLeech Whether user can leech
     * @param bool $protectIP Whether to protect user's IP
     * @return array Response from tracker
     */
    public static function addUser($userID, $passkey, $canLeech = true, $protectIP = false) {
        return self::sendRequest('add_user', [
            'user_id' => (int)$userID,
            'passkey' => $passkey,
            'can_leech' => (bool)$canLeech,
            'protect_ip' => (bool)$protectIP
        ]);
    }

    /**
     * Update user privileges
     *
     * @param string $passkey The user's passkey
     * @param bool|null $canLeech New can_leech value (null = no change)
     * @param bool|null $protectIP New protect_ip value (null = no change)
     * @return array Response from tracker
     */
    public static function updateUser($passkey, $canLeech = null, $protectIP = null) {
        $data = ['passkey' => $passkey];

        if ($canLeech !== null) {
            $data['can_leech'] = (bool)$canLeech;
        }
        if ($protectIP !== null) {
            $data['protect_ip'] = (bool)$protectIP;
        }

        return self::sendRequest('update_user', $data);
    }

    /**
     * Delete a user from the tracker
     *
     * @param string $passkey The user's passkey
     * @return array Response from tracker
     */
    public static function deleteUser($passkey) {
        return self::sendRequest('delete_user', [
            'passkey' => $passkey
        ]);
    }

    /**
     * Change a user's passkey
     *
     * @param string $oldPasskey Current passkey
     * @param string $newPasskey New passkey (32 chars)
     * @return array Response from tracker
     */
    public static function changePasskey($oldPasskey, $newPasskey) {
        return self::sendRequest('change_passkey', [
            'passkey' => $oldPasskey,
            'new_passkey' => $newPasskey
        ]);
    }

    /**
     * Add a freeleech token for a user on a torrent
     *
     * @param int $userID The user ID
     * @param string $infoHash The torrent info hash
     * @return array Response from tracker
     */
    public static function addToken($userID, $infoHash) {
        return self::sendRequest('add_token', [
            'user_id' => (int)$userID,
            'info_hash' => $infoHash
        ]);
    }

    /**
     * Remove a freeleech token from a user
     *
     * @param int $userID The user ID
     * @param string $infoHash The torrent info hash
     * @return array Response from tracker
     */
    public static function removeToken($userID, $infoHash) {
        return self::sendRequest('remove_token', [
            'user_id' => (int)$userID,
            'info_hash' => $infoHash
        ]);
    }

    /**
     * Add a peer_id prefix to the whitelist
     *
     * @param string $prefix The prefix to add
     * @return array Response from tracker
     */
    public static function addWhitelist($prefix) {
        return self::sendRequest('add_whitelist', [
            'peer_id_prefix' => $prefix
        ]);
    }

    /**
     * Remove a peer_id prefix from the whitelist
     *
     * @param string $prefix The prefix to remove
     * @return array Response from tracker
     */
    public static function removeWhitelist($prefix) {
        return self::sendRequest('remove_whitelist', [
            'peer_id_prefix' => $prefix
        ]);
    }

    /**
     * Get live tracker statistics
     *
     * @return array Stats including uptime, connections, announces, etc.
     */
    public static function getStats() {
        $url = self::$trackerUrl . '/' . self::$sitePassword . '/stats';

        $ch = curl_init($url);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_TIMEOUT, 5);

        $response = curl_exec($ch);
        curl_close($ch);

        return json_decode($response, true);
    }

    /**
     * Get list of active torrents
     *
     * @param int $limit Maximum number of torrents to return
     * @return array List of torrents
     */
    public static function getTorrents($limit = 100) {
        $url = self::$trackerUrl . '/' . self::$sitePassword . '/torrents?limit=' . $limit;

        $ch = curl_init($url);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_TIMEOUT, 5);

        $response = curl_exec($ch);
        curl_close($ch);

        return json_decode($response, true);
    }

    /**
     * Get list of peers for a torrent
     *
     * @param string $infoHash The torrent info hash
     * @param int $limit Maximum number of peers to return
     * @return array List of peers
     */
    public static function getPeers($infoHash, $limit = 100) {
        $url = self::$trackerUrl . '/' . self::$sitePassword . '/peers?info_hash=' . urlencode($infoHash) . '&limit=' . $limit;

        $ch = curl_init($url);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_TIMEOUT, 5);

        $response = curl_exec($ch);
        curl_close($ch);

        return json_decode($response, true);
    }

    /**
     * Get whitelist prefixes
     *
     * @return array List of whitelist prefixes
     */
    public static function getWhitelist() {
        $url = self::$trackerUrl . '/' . self::$sitePassword . '/whitelist';

        $ch = curl_init($url);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch, CURLOPT_TIMEOUT, 5);

        $response = curl_exec($ch);
        curl_close($ch);

        return json_decode($response, true);
    }
}
