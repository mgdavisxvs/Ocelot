<?php
/**
 * Markov Analytics Sidecar API Client
 *
 * Thin wrapper around the ocelot-markov HTTP API (default :9090).
 * All methods return decoded JSON or null on connectivity failure.
 */

class MarkovAPI {
    private static function get(string $path): ?array {
        $url = (defined('MARKOV_URL') ? MARKOV_URL : 'http://localhost:9090') . $path;

        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT        => 3,
            CURLOPT_CONNECTTIMEOUT => 2,
            CURLOPT_HTTPHEADER     => ['Accept: application/json'],
        ]);

        $body = curl_exec($ch);
        $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);

        if ($body === false || $httpCode !== 200) {
            return null;
        }

        return json_decode($body, true);
    }

    public static function health(): ?array {
        return self::get('/health');
    }

    public static function metrics(): ?array {
        return self::get('/metrics');
    }

    public static function chainPeer(): ?array {
        return self::get('/chain/peer');
    }

    public static function chainUser(): ?array {
        return self::get('/chain/user');
    }

    public static function chainTorrent(): ?array {
        return self::get('/chain/torrent');
    }

    public static function torrent(int $id): ?array {
        return self::get('/torrent/' . $id);
    }

    public static function userAnomaly(int $uid): ?array {
        return self::get('/user/' . $uid . '/anomaly');
    }

    public static function freeleech(): ?array {
        return self::get('/freeleech');
    }

    public static function seederPageRank(float $damping = 0.85): ?array {
        $q = $damping !== 0.85 ? '?damping=' . $damping : '';
        return self::get('/seeder/pagerank' . $q);
    }
}
