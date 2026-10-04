<?php

namespace PHP2Go\Extension\Standard;

/**
 * The request's superglobals, as the linked engine (embed SAPI,
 * auto_globals_jit, variables_order "EGPCS") creates them:
 *
 * - `$_GET`, `$_POST`, `$_COOKIE` and `$_FILES` always, empty, first;
 * - `$_SERVER`, `$_ENV` and `$_REQUEST` only when the program spells one, in
 *   the order its compile met them -- which only the compile knows, so the
 *   runtime hands the list over (`auto_globals()`).
 *
 * Defining is first-wins: a host or application initializer that ran before
 * this one keeps what it defined, and this fills in the rest.
 */
function register_superglobals_impl(): void
{
    foreach (['_GET', '_POST', '_COOKIE', '_FILES'] as $name) {
        \PHP2Go\Internal\register_superglobal($name, []);
    }
    foreach (\PHP2Go\Internal\auto_globals() as $name) {
        \PHP2Go\Internal\register_superglobal($name, auto_global_impl($name));
    }
    \PHP2Go\Internal\register_superglobal(
        '_RESP_HEADERS',
        ['X-Powered-By: PHP/' . PHP_VERSION]
    );
}

/**
 * The value the engine gives a just-in-time superglobal: `$_ENV` is the
 * environment; `$_SERVER` is the environment followed by the request time and
 * an empty `argv` (embed has no command line and no `PHP_SELF`); `$_REQUEST`
 * is empty, since request_order is empty and there is no request.
 */
function auto_global_impl(string $name): array
{
    if ($name === '_REQUEST') {
        return [];
    }
    $values = \getenv();
    if ($name === '_ENV') {
        return $values;
    }
    $wall = \PHP2Go\Internal\clock('wall');
    $values['REQUEST_TIME_FLOAT'] = $wall[0] + ($wall[1] / 1000000);
    $values['REQUEST_TIME'] = $wall[0];
    $values['argv'] = [];
    $values['argc'] = 0;
    return $values;
}

function response_headers_init_impl(): void
{
    \PHP2Go\Internal\register_superglobal(
        '_RESP_HEADERS',
        ['X-Powered-By: PHP/' . PHP_VERSION]
    );
}

function response_code_state_impl(bool $write = false, int $value = 0): int|bool
{
    static $code = false;
    if (!$write) {
        return $code;
    }
    $previous = $code;
    $code = $value;
    return $previous === false ? true : $previous;
}

function response_header_name_impl(string $header): ?string
{
    $colon = \strpos($header, ':');
    if ($colon === false) {
        return null;
    }
    return \strtolower(\substr($header, 0, $colon));
}

function response_header_add_impl(string $header, bool $replace): void
{
    response_headers_init_impl();
    $name = response_header_name_impl($header);
    if ($replace && $name !== null) {
        $kept = [];
        foreach ($_RESP_HEADERS as $held) {
            if (response_header_name_impl($held) !== $name) {
                $kept[] = $held;
            }
        }
        $_RESP_HEADERS = $kept;
    }
    $_RESP_HEADERS[] = $header;
}

function response_header_trim_impl(string $header): string
{
    $end = \strlen($header);
    while ($end > 0) {
        $byte = \ord($header[$end - 1]);
        if (!($byte === 32 || ($byte >= 9 && $byte <= 13))) {
            break;
        }
        --$end;
    }
    return \substr($header, 0, $end);
}

function response_header_invalid_impl(string $header): ?string
{
    for ($index = 0, $length = \strlen($header); $index < $length; ++$index) {
        if ($header[$index] === "\0") {
            return 'Header may not contain NUL bytes';
        }
        if ($header[$index] === "\r" || $header[$index] === "\n") {
            return 'Header may not contain more than a single header, new line detected';
        }
    }
    return null;
}

function header_impl(
    string $header,
    bool $replace = true,
    int $response_code = 0
): void {
    $header = response_header_trim_impl($header);
    if ($header === '') {
        return;
    }
    $invalid = response_header_invalid_impl($header);
    if ($invalid !== null) {
        \PHP2Go\Internal\warning($invalid);
        return;
    }

    if (\strncasecmp($header, 'HTTP/', 5) === 0) {
        response_code_state_impl(true, (int) \substr($header, 9, 3));
        return;
    }

    $name = response_header_name_impl($header);
    if ($name === 'content-type') {
        $colon = \strpos($header, ':');
        $value = \ltrim(\substr($header, $colon + 1));
        if (\strncasecmp($value, 'text/', 5) === 0 &&
            \strpos(\strtolower($value), 'charset=') === false) {
            $value .= ';charset=UTF-8';
        }
        $header = 'Content-type: ' . $value;
    }
    $current = response_code_state_impl();
    if ($response_code !== 0) {
        response_code_state_impl(true, $response_code);
    } elseif ($name === 'location' &&
        ($current === false || (($current < 300 || $current > 399) && $current !== 201))) {
        response_code_state_impl(true, 302);
    } elseif ($name === 'www-authenticate') {
        response_code_state_impl(true, 401);
    }

    response_header_add_impl($header, $replace);
}

function header_register_callback_impl(mixed $callback): bool
{
    // The linked embed SAPI reports headers as sent before application code.
    // PHP validates the callback, but deliberately does not retain or invoke
    // it when registration happens after that point. Public signature
    // validation runs before this aliased body.
    return true;
}

function header_remove_impl(?string $name = null): void
{
    response_headers_init_impl();
    if ($name === null) {
        $_RESP_HEADERS = [];
        return;
    }
    $name = response_header_trim_impl($name);
    if (\strpos($name, ':') !== false) {
        \PHP2Go\Internal\warning('Header to delete may not contain colon.');
        return;
    }
    $lower = \strtolower($name);
    $kept = [];
    foreach ($_RESP_HEADERS as $held) {
        if (response_header_name_impl($held) !== $lower) {
            $kept[] = $held;
        }
    }
    $_RESP_HEADERS = $kept;
}

function headers_list_impl(): array
{
    response_headers_init_impl();
    return $_RESP_HEADERS;
}

function headers_sent_impl(mixed &$filename = null, mixed &$line = null): bool
{
    $filename = '';
    $line = 0;
    return true;
}

function http_response_code_impl(int $response_code = 0): int|bool
{
    if ($response_code === 0) {
        return response_code_state_impl();
    }
    return response_code_state_impl(true, $response_code);
}

function cookie_illegal_impl(string $value, bool $name): bool
{
    $illegal = $name ? "=,; \t\r\n\v\f" : ",; \t\r\n\v\f";
    for ($index = 0, $length = \strlen($value); $index < $length; ++$index) {
        if (\strpos($illegal, $value[$index]) !== false) {
            return true;
        }
    }
    return false;
}

function cookie_date_impl(int $timestamp): string
{
    $days = \intdiv($timestamp, 86400);
    $seconds = $timestamp % 86400;
    $hour = \intdiv($seconds, 3600);
    $minute = \intdiv($seconds % 3600, 60);
    $second = $seconds % 60;

    $z = $days + 719468;
    $era = \intdiv($z, 146097);
    $day_of_era = $z - $era * 146097;
    $year_of_era = \intdiv(
        $day_of_era - \intdiv($day_of_era, 1460) +
        \intdiv($day_of_era, 36524) - \intdiv($day_of_era, 146096),
        365
    );
    $year = $year_of_era + $era * 400;
    $day_of_year = $day_of_era -
        (365 * $year_of_era + \intdiv($year_of_era, 4) -
         \intdiv($year_of_era, 100));
    $month_piece = \intdiv(5 * $day_of_year + 2, 153);
    $day = $day_of_year - \intdiv(153 * $month_piece + 2, 5) + 1;
    $month = $month_piece + ($month_piece < 10 ? 3 : -9);
    $year += $month <= 2 ? 1 : 0;

    $weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
    $months = [1 => 'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
        'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    $weekday = ($days + 4) % 7;
    return \sprintf(
        '%s, %02d %s %04d %02d:%02d:%02d GMT',
        $weekdays[$weekday],
        $day,
        $months[$month],
        $year,
        $hour,
        $minute,
        $second
    );
}

function setcookie_common_impl(
    string $function,
    bool $raw,
    string $name,
    string $value = '',
    array|int $expires_or_options = 0,
    string $path = '',
    string $domain = '',
    bool $secure = false,
    bool $httponly = false,
    int $argument_count = 0
): bool {
    if ($name === '') {
        throw new \ValueError(
            $function . '(): Argument #1 ($name) must not be empty'
        );
    }
    if (cookie_illegal_impl($name, true)) {
        throw new \ValueError(
            $function . '(): Argument #1 ($name) cannot contain "=", ",", ";", " ", "\\t", "\\r", "\\n", "\\013", or "\\014"'
        );
    }
    if ($raw && cookie_illegal_impl($value, false)) {
        throw new \ValueError(
            $function . '(): Argument #2 ($value) cannot contain ",", ";", " ", "\\t", "\\r", "\\n", "\\013", or "\\014"'
        );
    }

    $expires = 0;
    $samesite = '';
    if (\is_array($expires_or_options)) {
        if ($argument_count > 3) {
            throw new \ArgumentCountError(
                $function . '(): Expects exactly 3 arguments when argument #3 ' .
                '($expires_or_options) is an array'
            );
        }
        foreach ($expires_or_options as $key => $option) {
            if (\is_int($key)) {
                throw new \ValueError(
                    $function . '(): option array cannot have numeric keys'
                );
            }
            switch (\strtolower($key)) {
                case 'expires':
                    $expires = (int) $option;
                    break;
                case 'path':
                    $path = (string) $option;
                    break;
                case 'domain':
                    $domain = (string) $option;
                    break;
                case 'secure':
                    $secure = (bool) $option;
                    break;
                case 'httponly':
                    $httponly = (bool) $option;
                    break;
                case 'samesite':
                    $samesite = (string) $option;
                    break;
                default:
                    throw new \ValueError(
                        $function . '(): option "' . $key . '" is invalid'
                    );
            }
        }
    } else {
        $expires = $expires_or_options;
    }

    if (cookie_illegal_impl($path, false)) {
        throw new \ValueError(
            $function . '(): "path" option cannot contain ",", ";", " ", "\\t", "\\r", "\\n", "\\013", or "\\014"'
        );
    }
    if (cookie_illegal_impl($domain, false)) {
        throw new \ValueError(
            $function . '(): "domain" option cannot contain ",", ";", " ", "\\t", "\\r", "\\n", "\\013", or "\\014"'
        );
    }
    if ($expires >= 253402300800) {
        throw new \ValueError(
            $function . '(): "expires" option cannot have a year greater than 9999'
        );
    }

    if ($value === '') {
        $header = 'Set-Cookie: ' . $name .
            '=deleted; expires=Thu, 01 Jan 1970 00:00:01 GMT; Max-Age=0';
    } else {
        $header = 'Set-Cookie: ' . $name . '=' .
            ($raw ? $value : rawurlencode_impl($value));
        if ($expires > 0) {
            $now = (int) \PHP2Go\Internal\clock('wall');
            $header .= '; expires=' . cookie_date_impl($expires) .
                '; Max-Age=' . ($expires > $now ? $expires - $now : 0);
        }
    }
    if ($path !== '') {
        $header .= '; path=' . $path;
    }
    if ($domain !== '') {
        $header .= '; domain=' . $domain;
    }
    if ($secure) {
        $header .= '; secure';
    }
    if ($httponly) {
        $header .= '; HttpOnly';
    }
    if ($samesite !== '') {
        $header .= '; SameSite=' . $samesite;
    }
    response_header_add_impl($header, false);
    return true;
}

function setcookie_impl(
    string $name,
    string $value = '',
    array|int $expires_or_options = 0,
    string $path = '',
    string $domain = '',
    bool $secure = false,
    bool $httponly = false
): bool {
    return setcookie_common_impl(
        'setcookie', false, $name, $value, $expires_or_options,
        $path, $domain, $secure, $httponly, \func_num_args()
    );
}

function setrawcookie_impl(
    string $name,
    string $value = '',
    array|int $expires_or_options = 0,
    string $path = '',
    string $domain = '',
    bool $secure = false,
    bool $httponly = false
): bool {
    return setcookie_common_impl(
        'setrawcookie', true, $name, $value, $expires_or_options,
        $path, $domain, $secure, $httponly, \func_num_args()
    );
}

function dns_put_u16_impl(int $value): string
{
    return \chr(($value >> 8) & 255) . \chr($value & 255);
}

function dns_u16_impl(string $packet, int $offset): int
{
    return (\ord($packet[$offset]) << 8) | \ord($packet[$offset + 1]);
}

function dns_u32_impl(string $packet, int $offset): int
{
    return (\ord($packet[$offset]) << 24) |
        (\ord($packet[$offset + 1]) << 16) |
        (\ord($packet[$offset + 2]) << 8) |
        \ord($packet[$offset + 3]);
}

function dns_encode_name_impl(string $hostname): string|false
{
    if ($hostname === '.') {
        return "\0";
    }
    if (\substr($hostname, -1) === '.') {
        $hostname = \substr($hostname, 0, -1);
    }
    if ($hostname === '' || \strlen($hostname) > 253) {
        return false;
    }
    $encoded = '';
    foreach (\explode('.', $hostname) as $label) {
        $length = \strlen($label);
        if ($length === 0 || $length > 63) {
            return false;
        }
        $encoded .= \chr($length) . $label;
    }
    return $encoded . "\0";
}

function dns_decode_name_impl(string $packet, int &$offset): string|false
{
    $length = \strlen($packet);
    $cursor = $offset;
    $resume = null;
    $labels = [];
    for ($steps = 0; $steps < 128; ++$steps) {
        if ($cursor >= $length) {
            return false;
        }
        $size = \ord($packet[$cursor]);
        if (($size & 192) === 192) {
            if ($cursor + 1 >= $length) {
                return false;
            }
            if ($resume === null) {
                $resume = $cursor + 2;
            }
            $cursor = (($size & 63) << 8) | \ord($packet[$cursor + 1]);
            continue;
        }
        if (($size & 192) !== 0) {
            return false;
        }
        ++$cursor;
        if ($size === 0) {
            $offset = $resume ?? $cursor;
            return $labels === [] ? '.' : \implode('.', $labels);
        }
        if ($cursor + $size > $length) {
            return false;
        }
        $labels[] = \substr($packet, $cursor, $size);
        $cursor += $size;
    }
    return false;
}

function dns_resolver_servers_impl(): array
{
    static $servers = null;
    if ($servers !== null) {
        return $servers;
    }
    $servers = [];
    $config = \PHP2Go\Internal\read_file('/etc/resolv.conf');
    if (\is_string($config)) {
        foreach (\explode("\n", $config) as $line) {
            $line = \ltrim($line);
            if (\strncasecmp($line, 'nameserver', 10) !== 0) {
                continue;
            }
            $rest = \ltrim(\substr($line, 10));
            $end = \strlen($rest);
            for ($index = 0; $index < $end; ++$index) {
                $byte = \ord($rest[$index]);
                if ($byte === 32 || ($byte >= 9 && $byte <= 13) ||
                    $rest[$index] === '#' || $rest[$index] === ';') {
                    $end = $index;
                    break;
                }
            }
            $server = \substr($rest, 0, $end);
            if ($server !== '') {
                $servers[] = $server;
            }
        }
    }
    if ($servers === []) {
        $servers[] = '127.0.0.1';
    }
    return $servers;
}

function dns_server_uri_impl(string $transport, string $server): string
{
    if (\strpos($server, ':') !== false && $server[0] !== '[') {
        $server = '[' . $server . ']';
    }
    return $transport . '://' . $server . ':53';
}

function dns_read_exact_impl(mixed $stream, int $length): string|false
{
    $result = '';
    while (\strlen($result) < $length) {
        $part = \fread($stream, $length - \strlen($result));
        if ($part === false || $part === '') {
            return false;
        }
        $result .= $part;
    }
    return $result;
}

function dns_exchange_impl(string $query, int $identifier): string|false
{
    foreach (dns_resolver_servers_impl() as $server) {
        $errno = 0;
        $error = '';
        $socket = @\stream_socket_client(
            dns_server_uri_impl('udp', $server),
            $errno,
            $error,
            2.0,
            STREAM_CLIENT_CONNECT
        );
        if ($socket === false) {
            continue;
        }
        @\stream_set_timeout($socket, 2);
        $written = @\fwrite($socket, $query);
        $response = $written === \strlen($query)
            ? @\stream_socket_recvfrom($socket, 65535) : false;
        @\fclose($socket);
        if (!\is_string($response) || \strlen($response) < 12 ||
            dns_u16_impl($response, 0) !== $identifier) {
            continue;
        }
        if ((dns_u16_impl($response, 2) & 512) === 0) {
            return $response;
        }

        $socket = @\stream_socket_client(
            dns_server_uri_impl('tcp', $server),
            $errno,
            $error,
            2.0,
            STREAM_CLIENT_CONNECT
        );
        if ($socket === false) {
            continue;
        }
        @\stream_set_timeout($socket, 2);
        $framed = dns_put_u16_impl(\strlen($query)) . $query;
        $written = @\fwrite($socket, $framed);
        $prefix = $written === \strlen($framed)
            ? dns_read_exact_impl($socket, 2) : false;
        $response = $prefix === false ? false :
            dns_read_exact_impl($socket, dns_u16_impl($prefix, 0));
        @\fclose($socket);
        if (\is_string($response) && \strlen($response) >= 12 &&
            dns_u16_impl($response, 0) === $identifier) {
            return $response;
        }
    }
    return false;
}

function dns_query_impl(string $hostname, int $type): array|false
{
    $name = dns_encode_name_impl($hostname);
    if ($name === false) {
        return ['rcode' => 3, 'packet' => ''];
    }
    $identifier = \PHP2Go\Internal\mt_range(0, 65535);
    $query = dns_put_u16_impl($identifier) . "\1\0" .
        dns_put_u16_impl(1) . "\0\0\0\0\0\0" .
        $name . dns_put_u16_impl($type) . dns_put_u16_impl(1);
    $packet = dns_exchange_impl($query, $identifier);
    if ($packet === false) {
        return false;
    }
    return [
        'rcode' => dns_u16_impl($packet, 2) & 15,
        'packet' => $packet,
    ];
}

function dns_ipv6_impl(string $bytes): string
{
    $result = inet_ntop_impl($bytes);
    return $result === false ? '' : $result;
}

function dns_character_string_impl(
    string $packet,
    int &$offset,
    int $end
): string|false {
    if ($offset >= $end) {
        return false;
    }
    $length = \ord($packet[$offset++]);
    if ($offset + $length > $end) {
        return false;
    }
    $value = \substr($packet, $offset, $length);
    $offset += $length;
    return $value;
}

function dns_record_impl(
    string $packet,
    int &$offset,
    int $wanted,
    bool $raw
): array|false|null {
    $packet_length = \strlen($packet);
    $host = dns_decode_name_impl($packet, $offset);
    if ($host === false || $offset + 10 > $packet_length) {
        return false;
    }
    $type = dns_u16_impl($packet, $offset);
    $class = dns_u16_impl($packet, $offset + 2);
    $ttl = dns_u32_impl($packet, $offset + 4);
    $data_length = dns_u16_impl($packet, $offset + 8);
    $offset += 10;
    $start = $offset;
    $end = $start + $data_length;
    if ($end > $packet_length || $data_length === 0) {
        return false;
    }
    $offset = $end;
    if ($wanted !== 255 && $type !== $wanted) {
        return null;
    }

    $record = ['host' => $host, 'class' => 'IN', 'ttl' => $ttl];
    if ($raw) {
        $record['type'] = $type;
        $record['data'] = \substr($packet, $start, $data_length);
        return $record;
    }
    $cursor = $start;
    switch ($type) {
        case 1:
            if ($data_length < 4) {
                return false;
            }
            $record['type'] = 'A';
            $record['ip'] = \ord($packet[$start]) . '.' .
                \ord($packet[$start + 1]) . '.' .
                \ord($packet[$start + 2]) . '.' .
                \ord($packet[$start + 3]);
            return $record;
        case 2:
        case 5:
        case 12:
            $target = dns_decode_name_impl($packet, $cursor);
            if ($target === false) {
                return false;
            }
            $record['type'] = $type === 2 ? 'NS' : ($type === 5 ? 'CNAME' : 'PTR');
            $record['target'] = $target;
            return $record;
        case 6:
            $mname = dns_decode_name_impl($packet, $cursor);
            $rname = dns_decode_name_impl($packet, $cursor);
            if ($mname === false || $rname === false || $cursor + 20 > $end) {
                return false;
            }
            $record['type'] = 'SOA';
            $record['mname'] = $mname;
            $record['rname'] = $rname;
            foreach (['serial', 'refresh', 'retry', 'expire', 'minimum-ttl'] as $key) {
                $record[$key] = dns_u32_impl($packet, $cursor);
                $cursor += 4;
            }
            return $record;
        case 13:
            $cpu = dns_character_string_impl($packet, $cursor, $end);
            $os = dns_character_string_impl($packet, $cursor, $end);
            if ($cpu === false || $os === false) {
                return false;
            }
            $record['type'] = 'HINFO';
            $record['cpu'] = $cpu;
            $record['os'] = $os;
            return $record;
        case 15:
            if ($cursor + 2 > $end) {
                return false;
            }
            $record['type'] = 'MX';
            $record['pri'] = dns_u16_impl($packet, $cursor);
            $cursor += 2;
            $target = dns_decode_name_impl($packet, $cursor);
            if ($target === false) {
                return false;
            }
            $record['target'] = $target;
            return $record;
        case 16:
            $entries = [];
            $text = '';
            while ($cursor < $end) {
                $entry = dns_character_string_impl($packet, $cursor, $end);
                if ($entry === false) {
                    return false;
                }
                if ($entry !== '') {
                    $entries[] = $entry;
                    $text .= $entry;
                }
            }
            $record['type'] = 'TXT';
            $record['txt'] = $text;
            $record['entries'] = $entries;
            return $record;
        case 28:
            if ($data_length < 16) {
                return false;
            }
            $record['type'] = 'AAAA';
            $record['ipv6'] = dns_ipv6_impl(\substr($packet, $start, 16));
            return $record;
        case 33:
            if ($cursor + 6 > $end) {
                return false;
            }
            $record['type'] = 'SRV';
            $record['pri'] = dns_u16_impl($packet, $cursor);
            $record['weight'] = dns_u16_impl($packet, $cursor + 2);
            $record['port'] = dns_u16_impl($packet, $cursor + 4);
            $cursor += 6;
            $target = dns_decode_name_impl($packet, $cursor);
            if ($target === false) {
                return false;
            }
            $record['target'] = $target;
            return $record;
        case 35:
            if ($cursor + 4 > $end) {
                return false;
            }
            $record['type'] = 'NAPTR';
            $record['order'] = dns_u16_impl($packet, $cursor);
            $record['pref'] = dns_u16_impl($packet, $cursor + 2);
            $cursor += 4;
            foreach (['flags', 'services', 'regex'] as $key) {
                $value = dns_character_string_impl($packet, $cursor, $end);
                if ($value === false) {
                    return false;
                }
                $record[$key] = $value;
            }
            $replacement = dns_decode_name_impl($packet, $cursor);
            if ($replacement === false) {
                return false;
            }
            $record['replacement'] = $replacement;
            return $record;
        case 38:
            if ($cursor >= $end) {
                return false;
            }
            $mask = \ord($packet[$cursor++]);
            $suffix_bytes = \intdiv(128 - $mask + 7, 8);
            if ($cursor + $suffix_bytes > $end) {
                return false;
            }
            $record['type'] = 'A6';
            $record['masklen'] = $mask;
            $address = \str_repeat("\0", 16 - $suffix_bytes) .
                \substr($packet, $cursor, $suffix_bytes);
            $cursor += $suffix_bytes;
            $record['ipv6'] = dns_ipv6_impl($address);
            if ($cursor < $end) {
                $chain = dns_decode_name_impl($packet, $cursor);
                if ($chain === false) {
                    return false;
                }
                $record['chain'] = $chain;
            }
            return $record;
        case 257:
            if ($cursor + 2 > $end) {
                return false;
            }
            $tag_length = \ord($packet[$cursor + 1]);
            if ($cursor + 2 + $tag_length > $end) {
                return false;
            }
            $record['type'] = 'CAA';
            $record['flags'] = \ord($packet[$cursor]);
            $record['tag'] = \substr($packet, $cursor + 2, $tag_length);
            $record['value'] = \substr(
                $packet,
                $cursor + 2 + $tag_length,
                $end - $cursor - 2 - $tag_length
            );
            return $record;
    }
    return null;
}

function dns_parse_response_impl(
    string $packet,
    int $wanted,
    bool $raw
): array|false {
    if (\strlen($packet) < 12) {
        return false;
    }
    $counts = [
        dns_u16_impl($packet, 4),
        dns_u16_impl($packet, 6),
        dns_u16_impl($packet, 8),
        dns_u16_impl($packet, 10),
    ];
    $offset = 12;
    for ($index = 0; $index < $counts[0]; ++$index) {
        if (dns_decode_name_impl($packet, $offset) === false ||
            $offset + 4 > \strlen($packet)) {
            return false;
        }
        $offset += 4;
    }

    $sections = [[], [], []];
    for ($section = 0; $section < 3; ++$section) {
        for ($index = 0; $index < $counts[$section + 1]; ++$index) {
            $record = dns_record_impl(
                $packet,
                $offset,
                $section === 0 ? $wanted : 255,
                $raw
            );
            if ($record === false) {
                return false;
            }
            if ($record !== null) {
                $sections[$section][] = $record;
            }
        }
    }
    return $sections;
}

function dns_query_records_impl(
    string $hostname,
    int $type,
    bool $raw
): array|false {
    $result = dns_query_impl($hostname, $type);
    if ($result === false) {
        \PHP2Go\Internal\warning('DNS Query failed');
        return false;
    }
    if ($result['rcode'] === 2) {
        \PHP2Go\Internal\warning('A temporary server error occurred.');
        return false;
    }
    if ($result['rcode'] !== 0) {
        return [[], [], []];
    }
    $parsed = dns_parse_response_impl($result['packet'], $type, $raw);
    if ($parsed === false) {
        \PHP2Go\Internal\warning('Unable to parse DNS data received');
    }
    return $parsed;
}

function dns_check_record_common_impl(
    string $function,
    string $hostname,
    string $type
): bool {
    if ($hostname === '') {
        throw new \ValueError(
            $function . '(): Argument #1 ($hostname) must not be empty'
        );
    }
    $types = [
        'A' => 1, 'NS' => 2, 'CNAME' => 5, 'SOA' => 6, 'PTR' => 12,
        'MX' => 15, 'TXT' => 16, 'AAAA' => 28, 'SRV' => 33,
        'NAPTR' => 35, 'A6' => 38, 'CAA' => 257, 'ANY' => 255,
    ];
    $type = \strtoupper($type);
    if (!isset($types[$type])) {
        throw new \ValueError(
            $function . '(): Argument #2 ($type) must be a valid DNS record type'
        );
    }
    $records = dns_query_records_impl($hostname, $types[$type], false);
    return $records !== false && $records[0] !== [];
}

function dns_check_record_impl(string $hostname, string $type = 'MX'): bool
{
    return dns_check_record_common_impl('dns_check_record', $hostname, $type);
}

function checkdnsrr_impl(string $hostname, string $type = 'MX'): bool
{
    return dns_check_record_common_impl('checkdnsrr', $hostname, $type);
}

function dns_get_record_impl(
    string $hostname,
    int $type = DNS_ANY,
    mixed &$authoritative_name_servers = null,
    mixed &$additional_records = null,
    bool $raw = false
): array|false {
    $authoritative_name_servers = [];
    $additional_records = [];
    if (!$raw && (($type & ~DNS_ALL) !== 0) && $type !== DNS_ANY) {
        throw new \ValueError(
            'dns_get_record(): Argument #2 ($type) must be a DNS_* constant'
        );
    }
    if ($raw && ($type < 1 || $type > 65535)) {
        throw new \ValueError(
            'dns_get_record(): Argument #2 ($type) must be between 1 and 65535 when argument #5 ($raw) is true'
        );
    }

    $want_authority = \func_num_args() >= 3;
    $want_additional = \func_num_args() >= 4;
    if ($raw) {
        $queries = [[$type, $type]];
    } elseif ($type === DNS_ANY) {
        $queries = [[255, 255]];
    } else {
        $queries = [];
        foreach ([
            DNS_A => 1, DNS_NS => 2, DNS_CNAME => 5, DNS_SOA => 6,
            DNS_PTR => 12, DNS_HINFO => 13, DNS_MX => 15, DNS_TXT => 16,
            DNS_AAAA => 28, DNS_SRV => 33, DNS_NAPTR => 35,
            DNS_A6 => 38, DNS_CAA => 257,
        ] as $mask => $wire_type) {
            if (($type & $mask) !== 0) {
                $queries[] = [$wire_type, $wire_type];
            }
        }
    }

    $answer = [];
    foreach ($queries as [$wire_type, $wanted]) {
        $records = dns_query_records_impl($hostname, $wire_type, $raw);
        if ($records === false) {
            return false;
        }
        foreach ($records[0] as $record) {
            $answer[] = $record;
        }
        if ($want_authority) {
            foreach ($records[1] as $record) {
                $authoritative_name_servers[] = $record;
            }
        }
        if ($want_additional) {
            foreach ($records[2] as $record) {
                $additional_records[] = $record;
            }
        }
    }

    if ($want_additional && $type !== DNS_ANY) {
        $records = dns_query_records_impl($hostname, 255, $raw);
        if ($records === false) {
            return false;
        }
        foreach ($records[1] as $record) {
            $authoritative_name_servers[] = $record;
        }
        foreach ($records[2] as $record) {
            $additional_records[] = $record;
        }
    }
    return $answer;
}

function dns_get_mx_impl(
    string $hostname,
    mixed &$hosts,
    mixed &$weights = null
): bool {
    $hosts = [];
    $weights = [];
    $records = dns_query_records_impl($hostname, 15, false);
    if ($records === false) {
        return false;
    }
    foreach ($records[0] as $record) {
        if (($record['type'] ?? '') === 'MX') {
            $hosts[] = $record['target'];
            $weights[] = $record['pri'];
        }
    }
    return $hosts !== [];
}

function getmxrr_impl(
    string $hostname,
    mixed &$hosts,
    mixed &$weights = null
): bool {
    return dns_get_mx_impl($hostname, $hosts, $weights);
}

function gethostbyname_impl(string $hostname): string
{
    if (\strpos($hostname, "\0") !== false) {
        throw new \ValueError(
            'gethostbyname(): Argument #1 ($hostname) must not contain any null bytes'
        );
    }
    if (\strlen($hostname) > 255) {
        \PHP2Go\Internal\warning(
            'gethostbyname(): Host name cannot be longer than 255 characters'
        );
        return $hostname;
    }
    $result = \PHP2Go\Internal\network_database(
        'host-by-name',
        $hostname
    );
    if ($result === false) {
        \PHP2Go\Internal\warning(
            'gethostbyname(): Host name to ip failed ' . $hostname
        );
        return $hostname;
    }
    return $result;
}

function gethostbynamel_impl(string $hostname): array|false
{
    if (\strpos($hostname, "\0") !== false) {
        throw new \ValueError(
            'gethostbynamel(): Argument #1 ($hostname) must not contain any null bytes'
        );
    }
    if (\strlen($hostname) > 255) {
        \PHP2Go\Internal\warning(
            'gethostbynamel(): Host name cannot be longer than 255 characters'
        );
        return false;
    }
    return \PHP2Go\Internal\network_database(
        'host-by-name-list',
        $hostname
    );
}

function gethostbyaddr_impl(string $ip): string|false
{
    if (\strpos($ip, "\0") !== false) {
        throw new \ValueError(
            'gethostbyaddr(): Argument #1 ($ip) must not contain any null bytes'
        );
    }
    $result = \PHP2Go\Internal\network_database('host-by-address', $ip);
    if ($result === false) {
        \PHP2Go\Internal\warning(
            'gethostbyaddr(): Address is not in a.b.c.d form'
        );
    }
    return $result;
}

function getprotobyname_impl(string $protocol): int|false
{
    if (\strpos($protocol, "\0") !== false) {
        throw new \ValueError(
            'getprotobyname(): Argument #1 ($protocol) must not contain any null bytes'
        );
    }
    return \PHP2Go\Internal\network_database(
        'protocol-by-name',
        $protocol
    );
}

function getprotobynumber_impl(int $protocol): string|false
{
    return \PHP2Go\Internal\network_database(
        'protocol-by-number',
        $protocol
    );
}

function getservbyname_impl(string $service, string $protocol): int|false
{
    if (\strpos($service, "\0") !== false) {
        throw new \ValueError(
            'getservbyname(): Argument #1 ($service) must not contain any null bytes'
        );
    }
    if (\strpos($protocol, "\0") !== false) {
        throw new \ValueError(
            'getservbyname(): Argument #2 ($protocol) must not contain any null bytes'
        );
    }
    return \PHP2Go\Internal\network_database(
        'service-by-name',
        $service,
        $protocol
    );
}

function getservbyport_impl(int $port, string $protocol): string|false
{
    if (\strpos($protocol, "\0") !== false) {
        throw new \ValueError(
            'getservbyport(): Argument #2 ($protocol) must not contain any null bytes'
        );
    }
    return \PHP2Go\Internal\network_database(
        'service-by-port',
        $port,
        $protocol
    );
}

function net_get_interfaces_impl(): array|false
{
    $result = \PHP2Go\Internal\network_database('interfaces', null);
    if (\is_string($result)) {
        \PHP2Go\Internal\warning($result);
        return false;
    }
    return $result;
}

function array_rand_impl(mixed $array, mixed $num = 1): int|string|array
{
    $available = \count($array);
    if ($available === 0) {
        throw new \ValueError(
            'array_rand(): Argument #1 ($array) must not be empty'
        );
    }
    $num = (int) $num;
    if ($num <= 0 || $num > $available) {
        throw new \ValueError(
            'array_rand(): Argument #2 ($num) must be between 1 and the number of elements in argument #1 ($array)'
        );
    }
    $keys = \array_keys($array);
    if ($num === 1) {
        return \PHP2Go\Internal\array_random('key', $array);
    }

    $complement = $num > ($available >> 1);
    $wanted = $complement ? $available - $num : $num;
    $picked = [];
    while (\count($picked) < $wanted) {
        $picked[\PHP2Go\Internal\mt_range(0, $available - 1)] = true;
    }
    $result = [];
    foreach ($keys as $index => $key) {
        if (isset($picked[$index]) !== $complement) {
            $result[] = $key;
        }
    }
    return $result;
}

function shuffle_impl(mixed &$array): true
{
    $positions = [];
    for ($index = 0; $index < \count($array); ++$index) {
        $positions[] = $index;
    }
    for ($left = \count($positions) - 1; $left > 0; --$left) {
        $index = \PHP2Go\Internal\mt_range(0, $left);
        if ($index !== $left) {
            $temporary = $positions[$left];
            $positions[$left] = $positions[$index];
            $positions[$index] = $temporary;
        }
    }
    $array = \PHP2Go\Internal\array_random(
        'permute',
        $array,
        $positions
    );
    return true;
}

function getrusage_impl(mixed $mode = 0): array|false
{
    return \PHP2Go\Internal\resource_usage((int) $mode);
}

function ftok_impl(mixed $filename, mixed $project_id): int
{
    $filename = (string) $filename;
    $project_id = (string) $project_id;
    if (\strpos($filename, "\0") !== false) {
        throw new \ValueError(
            'ftok(): Argument #1 ($filename) must not contain any null bytes'
        );
    }
    if (\strlen($project_id) !== 1) {
        throw new \ValueError(
            'ftok(): Argument #2 ($project_id) must be a single character'
        );
    }
    $values = \PHP2Go\Internal\path_stat(
        $filename,
        true,
        false,
        'ftok'
    );
    if (!\is_array($values)) {
        \PHP2Go\Internal\warning(
            'ftok(): ftok() failed - ' . $values
        );
        return -1;
    }
    $key = (\ord($project_id) << 24)
        | (($values[0] & 0xff) << 16)
        | ($values[1] & 0xffff);
    if ($key > 0x7fffffff) {
        $key -= 0x100000000;
    }
    return $key;
}

function disk_space_impl(
    mixed $directory,
    string $function,
    int $index
): float|false {
    $directory = (string) $directory;
    if ($directory === '') {
        return false;
    }
    if (\strpos($directory, "\0") !== false) {
        throw new \ValueError(
            $function . '(): Argument #1 ($directory) must not contain any null bytes'
        );
    }
    $values = \PHP2Go\Internal\path_space($directory);
    if (!\is_array($values)) {
        \PHP2Go\Internal\warning($function . '(): ' . $values);
        return false;
    }
    return (float) $values[$index];
}

function disk_free_space_impl(mixed $directory): float|false
{
    return disk_space_impl($directory, 'disk_free_space', 0);
}

function disk_total_space_impl(mixed $directory): float|false
{
    return disk_space_impl($directory, 'disk_total_space', 1);
}

function diskfreespace_impl(mixed $directory): float|false
{
    return disk_space_impl($directory, 'diskfreespace', 0);
}

function path_predicate_impl(
    mixed $filename,
    string $function,
    string $predicate
): bool {
    return \PHP2Go\Internal\path_predicate(
        (string) $filename,
        $predicate,
        $function
    );
}

function file_exists_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'file_exists', 'exists');
}

function is_dir_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'is_dir', 'dir');
}

function is_executable_impl(mixed $filename): bool
{
    return path_predicate_impl(
        $filename,
        'is_executable',
        'executable'
    );
}

function is_file_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'is_file', 'file');
}

function is_link_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'is_link', 'link');
}

function is_readable_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'is_readable', 'readable');
}

function is_writable_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'is_writable', 'writable');
}

function is_writeable_impl(mixed $filename): bool
{
    return path_predicate_impl($filename, 'is_writeable', 'writable');
}

function path_stat_raw_impl(
    string $filename,
    string $function,
    bool $follow = true,
    bool $wrappers = true
): array|false {
    if ($filename === '') {
        return false;
    }
    if (\strpos($filename, "\0") !== false) {
        \PHP2Go\Internal\warning(
            $function . '(): Filename contains null byte'
        );
        return false;
    }
    $values = \PHP2Go\Internal\path_stat(
        $filename,
        $follow,
        $wrappers,
        $function
    );
    if (!\is_array($values)) {
        $operation = $follow ? 'stat' : 'Lstat';
        \PHP2Go\Internal\warning(
            $function . '(): ' . $operation . ' failed for ' . $filename
        );
        return false;
    }
    return $values;
}

function path_stat_result_impl(array $values): array
{
    $names = [
        'dev', 'ino', 'mode', 'nlink', 'uid', 'gid', 'rdev',
        'size', 'atime', 'mtime', 'ctime', 'blksize', 'blocks',
    ];
    $result = [];
    foreach ($values as $value) {
        $result[] = $value;
    }
    foreach ($names as $index => $name) {
        $result[$name] = $values[$index];
    }
    return $result;
}

function stat_impl(mixed $filename): array|false
{
    $values = path_stat_raw_impl((string) $filename, 'stat');
    return $values === false ? false : path_stat_result_impl($values);
}

function lstat_impl(mixed $filename): array|false
{
    $values = path_stat_raw_impl((string) $filename, 'lstat', false);
    return $values === false ? false : path_stat_result_impl($values);
}

function fileatime_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'fileatime', 8);
}

function filectime_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'filectime', 10);
}

function filegroup_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'filegroup', 5);
}

function fileinode_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'fileinode', 1);
}

function filemtime_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'filemtime', 9);
}

function fileowner_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'fileowner', 4);
}

function fileperms_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'fileperms', 2);
}

function filesize_impl(mixed $filename): int|false
{
    return path_stat_field_impl((string) $filename, 'filesize', 7);
}

function path_stat_field_impl(
    string $filename,
    string $function,
    int $index
): int|false {
    $values = path_stat_raw_impl($filename, $function);
    return $values === false ? false : $values[$index];
}

function filetype_impl(mixed $filename): string|false
{
    $values = path_stat_raw_impl((string) $filename, 'filetype', false);
    if ($values === false) {
        return false;
    }
    switch ($values[2] & 0170000) {
        case 0010000:
            return 'fifo';
        case 0020000:
            return 'char';
        case 0040000:
            return 'dir';
        case 0060000:
            return 'block';
        case 0100000:
            return 'file';
        case 0120000:
            return 'link';
        case 0140000:
            return 'socket';
        default:
            return 'unknown';
    }
}

function readlink_impl(mixed $path): string|false
{
    $path = (string) $path;
    if (\strpos($path, "\0") !== false) {
        throw new \ValueError(
            'readlink(): Argument #1 ($path) must not contain any null bytes'
        );
    }
    $result = \PHP2Go\Internal\read_link($path);
    if (!$result[0]) {
        \PHP2Go\Internal\warning("readlink(): " . $result[1]);
        return false;
    }
    return $result[1];
}

function linkinfo_impl(mixed $path): int|false
{
    $path = (string) $path;
    if (\strpos($path, "\0") !== false) {
        throw new \ValueError(
            'linkinfo(): Argument #1 ($path) must not contain any null bytes'
        );
    }
    $values = \PHP2Go\Internal\path_stat(
        $path,
        false,
        false,
        'linkinfo'
    );
    if (!\is_array($values)) {
        \PHP2Go\Internal\warning('linkinfo(): ' . $values);
        return -1;
    }
    return $values[0];
}

function path_mutation_result_impl(
    string $operation,
    string $filename,
    string|int|null $identity,
    array $result
): bool {
    if ($result[0]) {
        return true;
    }
    switch ($result[1]) {
        case 'wrapper':
            $base = $operation === 'lchown' ? 'chown'
                : ($operation === 'lchgrp' ? 'chgrp' : $operation);
            \PHP2Go\Internal\warning(
                $operation . '(): Cannot call ' . $base
                . '() for a non-standard stream'
            );
            break;
        case 'user':
            \PHP2Go\Internal\warning(
                $operation . '(): Unable to find uid for ' . $identity
            );
            break;
        case 'group':
            \PHP2Go\Internal\warning(
                $operation . '(): Unable to find gid for ' . $identity
            );
            break;
        case 'create':
            \PHP2Go\Internal\warning(
                'touch(): Unable to create file ' . $filename
                . ' because ' . $result[2]
            );
            break;
        case 'utime':
            \PHP2Go\Internal\warning('touch(): Utime failed: ' . $result[2]);
            break;
        default:
            \PHP2Go\Internal\warning($operation . '(): ' . $result[2]);
            break;
    }
    return false;
}

function path_mutation_validate_impl(string $filename, string $operation): void
{
    if (\strpos($filename, "\0") !== false) {
        throw new \ValueError(
            $operation . '(): Argument #1 ($filename) must not contain any null bytes'
        );
    }
}

function ownership_mutation_impl(
    string $filename,
    string|int $identity,
    string $operation
): bool {
    path_mutation_validate_impl($filename, $operation);
    return path_mutation_result_impl(
        $operation,
        $filename,
        $identity,
        \PHP2Go\Internal\path_mutation(
            $operation,
            $filename,
            $identity,
            false,
            0,
            0
        )
    );
}

function chown_impl(string $filename, string|int $user): bool
{
    return ownership_mutation_impl($filename, $user, 'chown');
}

function lchown_impl(string $filename, string|int $user): bool
{
    return ownership_mutation_impl($filename, $user, 'lchown');
}

function chgrp_impl(string $filename, string|int $group): bool
{
    return ownership_mutation_impl($filename, $group, 'chgrp');
}

function lchgrp_impl(string $filename, string|int $group): bool
{
    return ownership_mutation_impl($filename, $group, 'lchgrp');
}

function chmod_impl(string $filename, int $permissions): bool
{
    path_mutation_validate_impl($filename, 'chmod');
    return path_mutation_result_impl(
        'chmod',
        $filename,
        null,
        \PHP2Go\Internal\path_mutation(
            'chmod',
            $filename,
            $permissions,
            false,
            0,
            0
        )
    );
}

function touch_impl(
    string $filename,
    ?int $mtime = null,
    ?int $atime = null
): bool {
    path_mutation_validate_impl($filename, 'touch');
    if ($filename === '') {
        return false;
    }
    if ($mtime === null && $atime !== null) {
        throw new \ValueError(
            'touch(): Argument #2 ($mtime) cannot be null when argument #3 ($atime) is an integer'
        );
    }
    $set_times = $mtime !== null;
    if ($set_times && $atime === null) {
        $atime = $mtime;
    }
    return path_mutation_result_impl(
        'touch',
        $filename,
        null,
        \PHP2Go\Internal\path_mutation(
            'touch',
            $filename,
            0,
            $set_times,
            $mtime ?? 0,
            $atime ?? 0
        )
    );
}

function directory_default_impl(bool $write = false, mixed $value = null): mixed
{
    static $handle = null;
    if ($write) {
        $handle = $value;
    }
    return $handle;
}

function directory_path_validate_impl(string $directory, string $function): void
{
    if (\strpos($directory, "\0") !== false) {
        throw new \ValueError(
            $function . '(): Argument #1 ($directory) must not contain any null bytes'
        );
    }
}

function directory_context_validate_impl(
    mixed $context,
    string $function,
    int $argument
): void {
    if ($context === null) {
        return;
    }
    if (!\is_resource($context)) {
        if (\get_debug_type($context) === 'resource (closed)') {
            throw new \TypeError(
                $function . '(): supplied resource is not a valid Stream-Context resource'
            );
        }
        throw new \TypeError(
            $function . '(): Argument #' . $argument
            . ' ($context) must be of type resource or null, '
            . \get_debug_type($context) . ' given'
        );
    }
    if (\get_resource_type($context) !== 'stream-context') {
        throw new \TypeError(
            $function . '(): supplied resource is not a valid Stream-Context resource'
        );
    }
}

function directory_handle_impl(mixed $dir_handle, string $function): mixed
{
    if ($dir_handle === null) {
        $dir_handle = directory_default_impl();
        if ($dir_handle === null ||
            \get_debug_type($dir_handle) === 'resource (closed)') {
            directory_default_impl(true, null);
            throw new \TypeError('No resource supplied');
        }
    }
    if (!\is_resource($dir_handle) &&
        \get_debug_type($dir_handle) !== 'resource (closed)') {
        throw new \TypeError(
            $function . '(): Argument #1 ($dir_handle) '
            . 'must be of type resource or null, '
            . \get_debug_type($dir_handle) . ' given'
        );
    }
    return $dir_handle;
}

function opendir_impl(string $directory, mixed $context = null): mixed
{
    directory_path_validate_impl($directory, 'opendir');
    directory_context_validate_impl($context, 'opendir', 2);
    if ($directory === '') {
        return false;
    }
    $result = \PHP2Go\Internal\directory_operation(
        'open',
        $directory,
        'opendir',
        $context
    );
    if (!$result[0]) {
        \PHP2Go\Internal\warning(
            'opendir(' . $directory . '): Failed to open directory: '
            . $result[2]
        );
        return false;
    }
    directory_default_impl(true, $result[3]);
    return $result[3];
}

function dir_impl(string $directory, mixed $context = null): mixed
{
    directory_path_validate_impl($directory, 'dir');
    directory_context_validate_impl($context, 'dir', 2);
    if ($directory === '') {
        return false;
    }
    $result = \PHP2Go\Internal\directory_operation(
        'object',
        $directory,
        'dir',
        $context
    );
    if (!$result[0]) {
        \PHP2Go\Internal\warning(
            'dir(' . $directory . '): Failed to open directory: '
            . $result[2]
        );
        return false;
    }
    directory_default_impl(true, $result[3]);
    return $result[4];
}

function readdir_impl(mixed $dir_handle = null): string|false
{
    return \PHP2Go\Internal\directory_operation(
        'read',
        directory_handle_impl($dir_handle, 'readdir'),
        'readdir'
    );
}

function rewinddir_impl(mixed $dir_handle = null): void
{
    \PHP2Go\Internal\directory_operation(
        'rewind',
        directory_handle_impl($dir_handle, 'rewinddir'),
        'rewinddir'
    );
}

function closedir_impl(mixed $dir_handle = null): void
{
    $dir_handle = directory_handle_impl($dir_handle, 'closedir');
    $default = directory_default_impl();
    \PHP2Go\Internal\directory_operation(
        'close',
        $dir_handle,
        'closedir'
    );
    if ($dir_handle === $default) {
        directory_default_impl(true, null);
    }
}

function scandir_impl(
    string $directory,
    int $sorting_order = 0,
    mixed $context = null
): array|false {
    directory_path_validate_impl($directory, 'scandir');
    directory_context_validate_impl($context, 'scandir', 3);
    if ($directory === '') {
        throw new \ValueError(
            'scandir(): Argument #1 ($directory) must not be empty'
        );
    }
    $result = \PHP2Go\Internal\directory_operation(
        'list',
        $directory,
        'scandir',
        $context
    );
    if (!$result[0]) {
        \PHP2Go\Internal\warning(
            'scandir(' . $directory . '): Failed to open directory: '
            . $result[2]
        );
        $error = $result[1] === 0 ? 'Success' : $result[2];
        \PHP2Go\Internal\warning(
            'scandir(): (errno ' . $result[1] . '): ' . $error
        );
        return false;
    }
    $names = $result[3];
    if ($sorting_order === 0) {
        \usort($names, static fn(string $left, string $right): int =>
            \strcoll($left, $right));
    } elseif ($sorting_order !== 2) {
        \usort($names, static fn(string $left, string $right): int =>
            \strcoll($right, $left));
    }
    return $names;
}

function mkdir_impl(
    string $directory,
    int $permissions = 0777,
    bool $recursive = false,
    mixed $context = null
): bool {
    directory_path_validate_impl($directory, 'mkdir');
    directory_context_validate_impl($context, 'mkdir', 4);
    $result = \PHP2Go\Internal\path_topology(
        'mkdir', $directory, $permissions, $recursive, 'mkdir'
    );
    if (!$result[0] && $result[1] !== '') {
        \PHP2Go\Internal\warning('mkdir(): ' . $result[1]);
    }
    return $result[0];
}

function rmdir_impl(string $directory, mixed $context = null): bool
{
    directory_path_validate_impl($directory, 'rmdir');
    directory_context_validate_impl($context, 'rmdir', 2);
    $result = \PHP2Go\Internal\path_topology(
        'rmdir', $directory, 0, false, 'rmdir'
    );
    if (!$result[0] && $result[1] !== '') {
        \PHP2Go\Internal\warning(
            'rmdir(' . $directory . '): ' . $result[1]
        );
    }
    return $result[0];
}

function link_path_validate_impl(
    string $path,
    string $function,
    int $argument,
    string $name
): void {
    if (\strpos($path, "\0") !== false) {
        throw new \ValueError(
            $function . '(): Argument #' . $argument . ' ($' . $name
            . ') must not contain any null bytes'
        );
    }
}

function link_relation_impl(
    string $target,
    string $link,
    string $function
): bool {
    link_path_validate_impl($target, $function, 1, 'target');
    link_path_validate_impl($link, $function, 2, 'link');
    $result = \PHP2Go\Internal\path_relation($function, $target, $link);
    if (!$result[0]) {
        \PHP2Go\Internal\warning($function . '(): ' . $result[1]);
    }
    return $result[0];
}

function link_impl(string $target, string $link): bool
{
    return link_relation_impl($target, $link, 'link');
}

function symlink_impl(string $target, string $link): bool
{
    return link_relation_impl($target, $link, 'symlink');
}

function rename_impl(
    string $from,
    string $to,
    mixed $context = null
): bool {
    link_path_validate_impl($from, 'rename', 1, 'from');
    link_path_validate_impl($to, 'rename', 2, 'to');
    directory_context_validate_impl($context, 'rename', 3);
    $result = \PHP2Go\Internal\path_relation('rename', $from, $to);
    if ($result[3] !== '') {
        \PHP2Go\Internal\warning(
            'rename(' . $from . ',' . $to . '): ' . $result[3]
        );
    }
    if ($result[0]) {
        return true;
    }
    if ($result[2] === 'function') {
        \PHP2Go\Internal\warning('rename(): ' . $result[1]);
        return false;
    }
    if ($result[2] === 'directory') {
        \PHP2Go\Internal\warning(
            'rename(): The first argument to copy() function cannot be a directory'
        );
    }
    \PHP2Go\Internal\warning(
        'rename(' . $from . ',' . $to . '): ' . $result[1]
    );
    return false;
}

function umask_impl(?int $mask = null): int
{
    return \PHP2Go\Internal\process_umask($mask !== null, $mask ?? 0);
}

function copy_impl(
    string $from,
    string $to,
    mixed $context = null
): bool {
    link_path_validate_impl($from, 'copy', 1, 'from');
    link_path_validate_impl($to, 'copy', 2, 'to');
    directory_context_validate_impl($context, 'copy', 3);
    if ($from === '' || $to === '') {
        throw new \ValueError('Path must not be empty');
    }
    $result = \PHP2Go\Internal\copy_path($from, $to);
    if ($result[0] || $result[1] === 'silent') {
        return $result[0];
    }
    if ($result[1] === 'function') {
        if ($result[2] === 'Stream is not writable') {
            \PHP2Go\Internal\notice('copy(): ' . $result[2]);
        } else {
            \PHP2Go\Internal\warning('copy(): ' . $result[2]);
        }
    } else {
        $path = $result[1] === 'source' ? $from : $to;
        \PHP2Go\Internal\warning(
            'copy(' . $path . '): Failed to open stream: ' . $result[2]
        );
    }
    return false;
}

function tempnam_impl(string $directory, string $prefix): string|false
{
    link_path_validate_impl($directory, 'tempnam', 1, 'directory');
    link_path_validate_impl($prefix, 'tempnam', 2, 'prefix');
    $prefix = \rtrim($prefix, DIRECTORY_SEPARATOR);
    $prefix = $prefix === '' ? '' : \basename($prefix);
    if (\strlen($prefix) >= 64) {
        $prefix = \substr($prefix, 0, 63);
    }
    $result = \PHP2Go\Internal\temporary_file(
        'name',
        $directory,
        $prefix,
        sys_get_temp_dir_impl()
    );
    if ($result[2]) {
        \PHP2Go\Internal\notice(
            'tempnam(): file created in the system\'s temporary directory'
        );
    }
    return $result[0] ? $result[1] : false;
}

function tmpfile_impl(): mixed
{
    return \PHP2Go\Internal\temporary_file(
        'stream',
        '',
        'php',
        sys_get_temp_dir_impl()
    );
}

function file_impl(
    string $filename,
    int $flags = 0,
    $context = null
): array|false {
    file_path_nul_validate_impl($filename, 'file');
    if (($flags & ~7) !== 0) {
        throw new \ValueError(
            'file(): Argument #2 ($flags) must be a valid flag value'
        );
    }
    file_context_type_validate_impl($context, 'file');
    file_context_resource_validate_impl($context, 'file');
    $contents = file_read_bytes_impl(
        $filename,
        ($flags & 1) !== 0,
        'file'
    );
    if ($contents === false) {
        return false;
    }

    $ignore_new_lines = ($flags & 2) !== 0;
    $skip_empty_lines = ($flags & 4) !== 0;
    $lines = [];
    $length = \strlen($contents);
    $start = 0;
    while ($start < $length) {
        $newline = \strpos($contents, "\n", $start);
        if ($newline === false) {
            $line = \substr($contents, $start);
            $start = $length;
        } else {
            $line = \substr($contents, $start, $newline - $start + 1);
            $start = $newline + 1;
        }
        if ($ignore_new_lines && $newline !== false) {
            $line = \substr($line, 0, -1);
            if ($line !== '' && $line[\strlen($line) - 1] === "\r") {
                $line = \substr($line, 0, -1);
            }
        }
        if (!$skip_empty_lines || $line !== '') {
            $lines[] = $line;
        }
    }
    return $lines;
}

function readfile_impl(
    string $filename,
    bool $use_include_path = false,
    $context = null
): int|false {
    file_path_nul_validate_impl($filename, 'readfile');
    file_context_type_validate_impl($context, 'readfile');
    $contents = file_read_bytes_impl(
        $filename,
        $use_include_path,
        'readfile'
    );
    if ($contents === false) {
        return false;
    }
    echo $contents;
    file_context_resource_validate_impl($context, 'readfile');
    return \strlen($contents);
}

function md5_file_impl(string $filename, bool $binary = false): string|false
{
    return file_hash_impl($filename, $binary, 'md5_file');
}

function sha1_file_impl(string $filename, bool $binary = false): string|false
{
    return file_hash_impl($filename, $binary, 'sha1_file');
}

function file_hash_impl(
    string $filename,
    bool $binary,
    string $function
): string|false {
    file_path_nul_validate_impl($filename, $function);
    $contents = file_read_bytes_impl($filename, false, $function);
    if ($contents === false) {
        return false;
    }
    $digest = $function === 'md5_file'
        ? \md5($contents)
        : \sha1($contents);
    return $binary ? \hex2bin($digest) : $digest;
}

function get_meta_tags_impl(
    string $filename,
    bool $use_include_path = false
): array|false {
    file_path_nul_validate_impl($filename, 'get_meta_tags');
    $contents = file_read_bytes_impl(
        $filename,
        $use_include_path,
        'get_meta_tags'
    );
    if ($contents === false) {
        return false;
    }
    $nul = \strpos($contents, "\0");
    if ($nul !== false) {
        $contents = \substr($contents, 0, $nul);
    }

    $state = [
        'position' => 0,
        'lookahead' => null,
    ];
    $result = [];
    $last = 0;
    $in_tag = false;
    $in_meta = false;
    $looking_for_value = false;
    $saw_name = false;
    $saw_content = false;
    $have_name = false;
    $have_content = false;
    $name = null;
    $value = null;

    while (true) {
        [$token, $data] = meta_next_token_impl($contents, $state);
        if ($token === 0) {
            break;
        }

        if ($token === 6) {
            if ($last === 1) {
                $in_meta = \strcasecmp('meta', $data) === 0;
            } elseif ($last === 3 && $in_tag) {
                if (\strcasecmp('head', $data) === 0) {
                    break;
                }
            } elseif ($last === 4 && $looking_for_value) {
                if ($saw_name) {
                    $name = meta_name_impl($data);
                    $have_name = true;
                } elseif ($saw_content) {
                    $value = $data;
                    $have_content = true;
                }
                $looking_for_value = false;
            } elseif ($in_meta) {
                if (\strcasecmp('name', $data) === 0) {
                    $saw_name = true;
                    $saw_content = false;
                    $looking_for_value = true;
                } elseif (\strcasecmp('content', $data) === 0) {
                    $saw_name = false;
                    $saw_content = true;
                    $looking_for_value = true;
                }
            }
        } elseif (
            $token === 7 &&
            $last === 4 &&
            $looking_for_value
        ) {
            if ($saw_name) {
                $name = meta_name_impl($data);
                $have_name = true;
            } elseif ($saw_content) {
                $value = $data;
                $have_content = true;
            }
            $looking_for_value = false;
        } elseif ($token === 1) {
            if ($looking_for_value) {
                $looking_for_value = false;
                $have_name = false;
                $saw_name = false;
                $have_content = false;
                $saw_content = false;
            }
            $in_tag = true;
        } elseif ($token === 2) {
            if ($have_name) {
                $result[$name] = $have_content ? $value : '';
            }
            $name = null;
            $value = null;
            $in_tag = false;
            $in_meta = false;
            $looking_for_value = false;
            $have_name = false;
            $saw_name = false;
            $have_content = false;
            $saw_content = false;
        }
        $last = $token;
    }

    return $result;
}

function meta_next_token_impl(string $text, array &$state): array
{
    $length = \strlen($text);
    while (true) {
        if ($state['lookahead'] !== null) {
            $character = $state['lookahead'];
            $state['lookahead'] = null;
        } else {
            if ($state['position'] >= $length) {
                return [0, ''];
            }
            $character = $text[$state['position']];
            $state['position']++;
        }

        if ($character === '<') {
            return [1, ''];
        }
        if ($character === '>') {
            return [2, ''];
        }
        if ($character === '/') {
            return [3, ''];
        }
        if ($character === '=') {
            return [4, ''];
        }
        if ($character === ' ') {
            return [5, ''];
        }
        if (
            $character === "\n" ||
            $character === "\r" ||
            $character === "\t"
        ) {
            continue;
        }
        if ($character === "'" || $character === '"') {
            $quote = $character;
            $data = '';
            while ($state['position'] < $length) {
                $character = $text[$state['position']];
                $state['position']++;
                if ($character === $quote) {
                    break;
                }
                if ($character === '<' || $character === '>') {
                    $state['lookahead'] = $character;
                    break;
                }
                $data .= $character;
                if (\strlen($data) === 8192) {
                    break;
                }
            }
            return [7, $data];
        }
        if (meta_ascii_alnum_impl($character)) {
            $data = $character;
            while ($state['position'] < $length) {
                $character = $text[$state['position']];
                $state['position']++;
                if (meta_id_character_impl($character)) {
                    $data .= $character;
                    if (\strlen($data) === 8192) {
                        if (
                            !meta_ascii_alpha_impl($character) &&
                            $character !== '-'
                        ) {
                            $state['lookahead'] = $character;
                        }
                        break;
                    }
                    continue;
                }
                if (
                    !meta_ascii_alpha_impl($character) &&
                    $character !== '-'
                ) {
                    $state['lookahead'] = $character;
                }
                break;
            }
            return [6, $data];
        }
        return [8, ''];
    }
}

function meta_id_character_impl(string $character): bool
{
    return meta_ascii_alnum_impl($character) ||
        \strpos('-_.:', $character) !== false;
}

function meta_ascii_alnum_impl(string $character): bool
{
    return meta_ascii_alpha_impl($character) ||
        ($character >= '0' && $character <= '9');
}

function meta_ascii_alpha_impl(string $character): bool
{
    return ($character >= 'A' && $character <= 'Z') ||
        ($character >= 'a' && $character <= 'z');
}

function meta_name_impl(string $name): string
{
    $result = '';
    $unsafe = ".\\+*?[^]$() ";
    $length = \strlen($name);
    for ($index = 0; $index < $length; $index++) {
        $character = $name[$index];
        $result .= \strpos($unsafe, $character) !== false
            ? '_'
            : \strtolower($character);
    }
    return $result;
}

function file_read_bytes_impl(
    string $filename,
    bool $use_include_path,
    string $function
): string|false {
    if ($filename === '') {
        throw new \ValueError('Path must not be empty');
    }
    if ($use_include_path) {
        foreach (\explode(PATH_SEPARATOR, \get_include_path()) as $directory) {
            if ($directory === '' || $directory === '.') {
                $candidate = $filename;
            } else {
                $candidate = \rtrim($directory, '/\\') .
                    DIRECTORY_SEPARATOR . $filename;
            }
            $contents = \PHP2Go\Internal\read_file($candidate);
            if ($contents !== false) {
                return $contents;
            }
        }
    }
    $contents = \PHP2Go\Internal\read_file($filename);
    if ($contents === false) {
        \PHP2Go\Internal\warning(
            $function . '(' . $filename .
            '): Failed to open stream: No such file or directory'
        );
    }
    return $contents;
}

function file_path_nul_validate_impl(
    string $filename,
    string $function,
    int $argument = 1
): void {
    if (\strpos($filename, "\0") !== false) {
        throw new \ValueError(
            $function .
            '(): Argument #' . $argument .
            ' ($filename) must not contain any null bytes'
        );
    }
}

function file_context_type_validate_impl($context, string $function): void
{
    if ($context !== null && !\is_resource($context)) {
        throw new \TypeError(
            $function .
            '(): Argument #3 ($context) must be of type resource or null, ' .
            \get_debug_type($context) . ' given'
        );
    }
}

function file_context_resource_validate_impl($context, string $function): void
{
    if ($context !== null &&
        \get_resource_type($context) !== 'stream-context') {
        throw new \TypeError(
            $function .
            '(): supplied resource is not a valid Stream-Context resource'
        );
    }
}

function iptcparse_impl(string $iptc_block): array|false
{
    $size = \strlen($iptc_block);
    $position = 0;
    while ($position < $size) {
        if (
            $iptc_block[$position] === "\x1c" &&
            $position + 1 < $size
        ) {
            $record = \ord($iptc_block[$position + 1]);
            if ($record === 1 || $record === 2) {
                break;
            }
        }
        $position++;
    }

    $result = [];
    while ($position < $size) {
        if ($iptc_block[$position] !== "\x1c") {
            break;
        }
        $position++;
        if ($position + 4 >= $size) {
            break;
        }

        $record = \ord($iptc_block[$position]);
        $dataset = \ord($iptc_block[$position + 1]);
        $position += 2;
        if ((\ord($iptc_block[$position]) & 0x80) !== 0) {
            if ($position + 6 >= $size) {
                break;
            }
            $length =
                \ord($iptc_block[$position + 2]) * 16777216 +
                \ord($iptc_block[$position + 3]) * 65536 +
                \ord($iptc_block[$position + 4]) * 256 +
                \ord($iptc_block[$position + 5]);
            $position += 6;
        } else {
            $length =
                \ord($iptc_block[$position]) * 256 +
                \ord($iptc_block[$position + 1]);
            $position += 2;
        }

        if ($length > $size || $position + $length > $size) {
            break;
        }
        $key = $record . '#' .
            \str_pad((string) $dataset, 3, '0', STR_PAD_LEFT);
        $result[$key][] = \substr($iptc_block, $position, $length);
        $position += $length;
    }

    return $result === [] ? false : $result;
}

function iptcembed_impl(
    string $iptc_data,
    string $filename,
    int $spool = 0
): string|bool {
    file_path_nul_validate_impl($filename, 'iptcembed', 2);
    $jpeg = \PHP2Go\Internal\read_file($filename);
    if ($jpeg === false) {
        \PHP2Go\Internal\warning(
            'iptcembed(): Unable to open ' . $filename
        );
        return false;
    }

    $size = \strlen($jpeg);
    if ($size === 0 || \ord($jpeg[0]) !== 0xff) {
        if ($spool > 0 && $size > 0) {
            echo $jpeg[0];
        }
        return false;
    }
    if ($size < 2 || \ord($jpeg[1]) !== 0xd8) {
        if ($spool > 0) {
            echo \substr($jpeg, 0, 2);
        }
        return false;
    }

    $app13 = iptc_app13_impl($iptc_data);
    $output = iptc_byte_impl(255) . iptc_byte_impl(216);
    $position = 2;
    $written = false;
    while ($position < $size) {
        $marker_start = \strpos($jpeg, iptc_byte_impl(255), $position);
        if ($marker_start === false) {
            $output .= \substr($jpeg, $position);
            break;
        }

        $cursor = $marker_start + 1;
        while ($cursor < $size && \ord($jpeg[$cursor]) === 255) {
            $cursor++;
        }
        $output .= \substr($jpeg, $position, $cursor - $position);
        if ($cursor >= $size) {
            break;
        }

        $marker = \ord($jpeg[$cursor]);
        $position = $cursor + 1;
        if ($marker === 0xd9) {
            break;
        }
        if ($marker !== 0xed) {
            $output .= $jpeg[$cursor];
        }

        if ($marker === 0xed) {
            if ($position + 2 > $size) {
                break;
            }
            $segment_length =
                \ord($jpeg[$position]) * 256 +
                \ord($jpeg[$position + 1]);
            if ($segment_length < 2) {
                $position = $size;
            } else {
                $position += $segment_length;
                if ($position > $size) {
                    $position = $size;
                }
            }
            if ($position < $size) {
                $position++;
            }
            $output .= \substr($jpeg, $position);
            break;
        }

        if ($marker === 0xda) {
            $output .= \substr($jpeg, $position);
            break;
        }

        if ($position + 2 > $size) {
            $output .= \substr($jpeg, $position);
            break;
        }
        $segment_length =
            \ord($jpeg[$position]) * 256 +
            \ord($jpeg[$position + 1]);
        if (
            $segment_length < 2 ||
            $position + $segment_length > $size
        ) {
            $output .= \substr($jpeg, $position);
            break;
        }
        $output .= \substr($jpeg, $position, $segment_length);
        $position += $segment_length;

        if (
            !$written &&
            ($marker === 0xe0 || $marker === 0xe1)
        ) {
            $output .= $app13;
            $written = true;
        }
    }

    if ($spool > 0) {
        echo $output;
    }
    return $spool < 2 ? $output : true;
}

function iptc_app13_impl(string $iptc_data): string
{
    if ((\strlen($iptc_data) & 1) !== 0) {
        $iptc_data .= "\0";
    }
    $size = \strlen($iptc_data);
    $segment_length = $size + 28;
    return iptc_byte_impl(255) . iptc_byte_impl(237) .
        \chr(($segment_length >> 8) & 255) .
        \chr($segment_length & 255) .
        "Photoshop 3.0\0" .
        "8BIM\x04\x04\0\0\0\0" .
        \chr(($size >> 8) & 255) .
        \chr($size & 255) .
        $iptc_data;
}

function iptc_byte_impl(int $byte): string
{
    return \chr($byte);
}

function array_all_impl(array $array, callable $callback): bool
{
    foreach ($array as $key => $value) {
        if (!$callback($value, $key)) {
            return false;
        }
    }

    return true;
}

function array_any_impl(array $array, callable $callback): bool
{
    foreach ($array as $key => $value) {
        if ($callback($value, $key)) {
            return true;
        }
    }

    return false;
}

function array_change_key_case_impl(array $array, int $case = CASE_LOWER): array
{
    $result = [];
    foreach ($array as $key => $value) {
        if (\is_string($key)) {
            $key = $case ? \strtoupper($key) : \strtolower($key);
        }
        $result[$key] = $value;
    }

    return $result;
}

function array_find_impl(array $array, callable $callback): mixed
{
    foreach ($array as $key => $value) {
        if ($callback($value, $key)) {
            return $value;
        }
    }

    return null;
}

function array_find_key_impl(array $array, callable $callback): int|string|null
{
    foreach ($array as $key => $value) {
        if ($callback($value, $key)) {
            return $key;
        }
    }

    return null;
}

function array_multisort_impl(mixed &$array, mixed &...$rest): true
{
    $arguments = [];
    $arguments[] =& $array;
    foreach ($rest as &$argument) {
        $arguments[] =& $argument;
    }
    unset($argument);

    $columns = [];
    $directions = [];
    $types = [];
    $order_seen = [];
    $type_seen = [];
    $current = -1;

    foreach ($arguments as $offset => &$argument) {
        $position = $offset + 1;
        if (\is_array($argument)) {
            $current++;
            $columns[$current] =& $argument;
            $directions[$current] = SORT_ASC;
            $types[$current] = SORT_REGULAR;
            $order_seen[$current] = false;
            $type_seen[$current] = false;
            continue;
        }

        $slot = '#' . $position .
            ($position === 1 ? ' ($array)' : '');
        if (!\is_int($argument)) {
            throw new \TypeError(
                'array_multisort(): Argument ' . $slot .
                ' must be an array or a sort flag'
            );
        }

        if ($argument === SORT_ASC || $argument === SORT_DESC) {
            if ($current < 0 || $order_seen[$current]) {
                throw new \TypeError(
                    'array_multisort(): Argument ' . $slot .
                    ' must be an array or a sort flag that has not already been specified'
                );
            }
            $directions[$current] = $argument;
            $order_seen[$current] = true;
            continue;
        }

        $base = $argument & ~SORT_FLAG_CASE;
        $valid_type = (
            $base === SORT_REGULAR ||
            $base === SORT_NUMERIC ||
            $base === SORT_STRING ||
            $base === SORT_LOCALE_STRING ||
            $base === SORT_NATURAL
        ) && (
            $argument === $base ||
            $argument === ($base | SORT_FLAG_CASE)
        );
        if (!$valid_type) {
            throw new \ValueError(
                'array_multisort(): Argument ' . $slot .
                ' must be a valid sort flag'
            );
        }
        if ($current < 0 || $type_seen[$current]) {
            throw new \TypeError(
                'array_multisort(): Argument ' . $slot .
                ' must be an array or a sort flag that has not already been specified'
            );
        }
        $types[$current] = $argument;
        $type_seen[$current] = true;
    }
    unset($argument);

    $size = \count($columns[0]);
    foreach ($columns as $column) {
        if (\count($column) !== $size) {
            throw new \ValueError('Array sizes are inconsistent');
        }
    }

    $keys = [];
    $identities = [];
    foreach ($columns as &$column) {
        $keys[] = \array_keys($column);
        $identities[] = \PHP2Go\Internal\array_id($column);
    }
    unset($column);
    $positions = [];
    for ($position = 0; $position < $size; $position++) {
        $positions[] = $position;
    }
    \usort(
        $positions,
        static function (int $left, int $right) use (
            &$columns,
            $keys,
            $directions,
            $types
        ): int {
            $count = \count($columns);
            for ($column = 0; $column < $count; $column++) {
                $order = array_multisort_compare_impl(
                    $columns[$column][$keys[$column][$left]],
                    $columns[$column][$keys[$column][$right]],
                    $types[$column]
                );
                if ($order !== 0) {
                    return $directions[$column] === SORT_DESC
                        ? -$order
                        : $order;
                }
            }
            return 0;
        }
    );

    $processed = [];
    foreach ($columns as $column_index => &$column) {
        $identity = $identities[$column_index];
        if (isset($processed[$identity])) {
            continue;
        }
        $processed[$identity] = true;
        $ranks = [];
        foreach ($positions as $rank => $old_position) {
            $ranks[$keys[$column_index][$old_position]] = $rank;
        }
        \uksort(
            $column,
            static fn(int|string $left, int|string $right): int =>
                $ranks[$left] <=> $ranks[$right]
        );
        $column = \array_merge($column);
    }
    unset($column);
    return true;
}

function array_multisort_compare_impl(
    mixed $left,
    mixed $right,
    int $type
): int {
    $base = $type & ~SORT_FLAG_CASE;
    $case_insensitive = ($type & SORT_FLAG_CASE) !== 0;
    if ($base === SORT_REGULAR) {
        $order = $left <=> $right;
    } elseif ($base === SORT_NUMERIC) {
        $order = array_multisort_number_impl($left) <=>
            array_multisort_number_impl($right);
    } elseif ($base === SORT_NATURAL) {
        $order = $case_insensitive
            ? \strnatcasecmp((string) $left, (string) $right)
            : \strnatcmp((string) $left, (string) $right);
    } elseif ($base === SORT_STRING && $case_insensitive) {
        $order = \strcasecmp((string) $left, (string) $right);
    } else {
        // The linked build's only reachable locale is C, where locale string
        // order is byte-string order. The case bit is ignored for locale mode.
        $order = \strcmp((string) $left, (string) $right);
    }
    return $order < 0 ? -1 : ($order > 0 ? 1 : 0);
}

function array_multisort_number_impl(mixed $value): int|float
{
    return (float) $value;
}

function array_replace_impl(array $array, array ...$replacements): array
{
    $result = [];
    foreach ($array as $key => $value) {
        $result[$key] = $value;
    }

    foreach ($replacements as $replacement) {
        foreach ($replacement as $key => $value) {
            $result[$key] = $value;
        }
    }

    return $result;
}

function array_replace_recursive_impl(array $array, array ...$replacements): array
{
    // Build a fresh result rather than mutating the by-value argument. This is
    // equivalent under PHP copy-on-write and avoids sharing a nested array with
    // the caller in runtimes whose array copy is not recursive.
    $result = [];
    foreach ($array as $key => $value) {
        $result[$key] = $value;
    }

    foreach ($replacements as $replacement) {
        foreach ($replacement as $key => $value) {
            if (\array_key_exists($key, $result) && \is_array($result[$key]) && \is_array($value)) {
                $nested = $result[$key];
                $result[$key] = array_replace_recursive_impl($nested, $value);
            } else {
                $result[$key] = $value;
            }
        }
    }

    return $result;
}

function array_walk_recursive_impl(
    array|object &$array,
    callable $callback,
    mixed $arg = null
): true {
    $stack = [];
    array_walk_recursive_container_impl(
        $array,
        $callback,
        $arg,
        \func_num_args() === 3,
        $stack,
        false
    );
    return true;
}

function array_walk_recursive_container_impl(
    array|object &$container,
    callable $callback,
    mixed $arg,
    bool $has_arg,
    array &$stack,
    bool $protect
): void {
    $identity = null;
    if ($protect) {
        $identity = \PHP2Go\Internal\array_id($container);
        if (isset($stack[$identity])) {
            throw new \Error('Recursion detected');
        }
        $stack[$identity] = true;
    }

    try {
        \array_walk(
            $container,
            static function (&$value, int|string $key) use (
                $callback,
                $arg,
                $has_arg,
                &$stack
            ): void {
                if (\is_array($value)) {
                    array_walk_recursive_container_impl(
                        $value,
                        $callback,
                        $arg,
                        $has_arg,
                        $stack,
                        true
                    );
                } else {
                    $leaf = [];
                    $leaf[$key] =& $value;
                    if ($has_arg) {
                        \array_walk($leaf, $callback, $arg);
                    } else {
                        \array_walk($leaf, $callback);
                    }
                    unset($leaf[$key]);
                }
            }
        );
    } finally {
        if ($identity !== null) {
            unset($stack[$identity]);
        }
    }
}

function array_diff_uassoc_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_diff_uassoc', $array, $rest, 1);
    $deprecated = false;
    $key_compare = array_user_comparator_impl($callbacks[0], 'array_diff_uassoc', $deprecated);
    return array_user_diff_sorted_impl($arrays, 'assoc', null, $key_compare);
}

function array_diff_ukey_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_diff_ukey', $array, $rest, 1);
    $deprecated = false;
    $key_compare = array_user_comparator_impl($callbacks[0], 'array_diff_ukey', $deprecated);
    return array_user_diff_sorted_impl($arrays, 'key', null, $key_compare);
}

function array_intersect_uassoc_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_intersect_uassoc', $array, $rest, 1);
    $deprecated = false;
    $key_compare = array_user_comparator_impl($callbacks[0], 'array_intersect_uassoc', $deprecated);
    return array_user_intersect_sorted_impl($arrays, 'assoc', null, $key_compare);
}

function array_intersect_ukey_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_intersect_ukey', $array, $rest, 1);
    $deprecated = false;
    $key_compare = array_user_comparator_impl($callbacks[0], 'array_intersect_ukey', $deprecated);
    return array_user_intersect_sorted_impl($arrays, 'key', null, $key_compare);
}

function array_udiff_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_udiff', $array, $rest, 1);
    $deprecated = false;
    $data_compare = array_user_comparator_impl($callbacks[0], 'array_udiff', $deprecated);
    return array_user_diff_sorted_impl($arrays, 'normal', $data_compare, null);
}

function array_udiff_assoc_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_udiff_assoc', $array, $rest, 1);
    return array_user_diff_direct_impl($arrays, $callbacks[0]);
}

function array_udiff_uassoc_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_udiff_uassoc', $array, $rest, 2);
    $deprecated = false;
    $data_compare = array_user_comparator_impl($callbacks[0], 'array_udiff_uassoc', $deprecated);
    $key_compare = array_user_comparator_impl($callbacks[1], 'array_udiff_uassoc', $deprecated);
    return array_user_diff_sorted_impl($arrays, 'assoc', $data_compare, $key_compare);
}

function array_uintersect_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_uintersect', $array, $rest, 1);
    $deprecated = false;
    $data_compare = array_user_comparator_impl($callbacks[0], 'array_uintersect', $deprecated);
    return array_user_intersect_sorted_impl($arrays, 'normal', $data_compare, null);
}

function array_uintersect_assoc_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_uintersect_assoc', $array, $rest, 1);
    return array_user_intersect_direct_impl($arrays, $callbacks[0]);
}

function array_uintersect_uassoc_impl(array $array, ...$rest): array
{
    [$arrays, $callbacks] = array_user_arguments_impl('array_uintersect_uassoc', $array, $rest, 2);
    $deprecated = false;
    $data_compare = array_user_comparator_impl($callbacks[0], 'array_uintersect_uassoc', $deprecated);
    $key_compare = array_user_comparator_impl($callbacks[1], 'array_uintersect_uassoc', $deprecated);
    return array_user_intersect_sorted_impl($arrays, 'assoc', $data_compare, $key_compare);
}

function array_user_arguments_impl(
    string $name,
    array $array,
    array $rest,
    int $callback_count
): array {
    $total = 1 + \count($rest);
    $minimum = 1 + $callback_count;
    if ($total < $minimum) {
        $plural = $minimum === 1 ? '' : 's';
        throw new \ArgumentCountError(
            $name . '() expects at least ' . $minimum . ' argument' . $plural . ', ' . $total . ' given'
        );
    }

    $callback_start = \count($rest) - $callback_count;
    $callbacks = [];
    for ($index = 0; $index < $callback_count; $index++) {
        $position = 2 + $callback_start + $index;
        $callback = $rest[$callback_start + $index];
        \PHP2Go\Internal\validate_callback($callback, $name, $position);
        $callbacks[] = $callback;
    }

    $arrays = [$array];
    for ($index = 0; $index < $callback_start; $index++) {
        $candidate = $rest[$index];
        if (!\is_array($candidate)) {
            throw new \TypeError(
                $name . '(): Argument #' . (2 + $index) .
                ' must be of type array, ' . \get_debug_type($candidate) . ' given'
            );
        }
        $arrays[] = $candidate;
    }
    return [$arrays, $callbacks];
}

function array_user_comparator_impl(
    mixed $callback,
    string $name,
    bool &$deprecated
): \Closure {
    return static function (mixed $first, mixed $second) use (
        $callback,
        $name,
        &$deprecated
    ): int {
        $result = \call_user_func($callback, $first, $second);
        if (\is_bool($result)) {
            if (!$deprecated) {
                \PHP2Go\Internal\deprecated(
                    $name . '(): Returning bool from comparison function is deprecated, ' .
                    'return an integer less than, equal to, or greater than zero'
                );
                $deprecated = true;
            }
            if ($result) {
                return 1;
            }
            $reverse = (int) \call_user_func($callback, $second, $first);
            return -(($reverse < 0) ? -1 : (($reverse > 0) ? 1 : 0));
        }
        $result = (int) $result;
        return ($result < 0) ? -1 : (($result > 0) ? 1 : 0);
    };
}

function array_user_string_compare_impl(mixed $first, mixed $second): int
{
    $result = \strcmp((string) $first, (string) $second);
    return ($result < 0) ? -1 : (($result > 0) ? 1 : 0);
}

function array_user_direct_compare_impl(mixed $callback, mixed $first, mixed $second): int
{
    $result = (int) \call_user_func($callback, $first, $second);
    return ($result < 0) ? -1 : (($result > 0) ? 1 : 0);
}

function array_user_sorted_entries_impl(
    array $array,
    \Closure $compare,
    bool $by_key
): array {
    $sorted = \PHP2Go\Internal\sort_entries($array, $compare, $by_key, false);
    $entries = [];
    foreach ($sorted as $key => $value) {
        $entries[] = [$key, $value];
    }
    return $entries;
}

function array_user_diff_direct_impl(array $arrays, mixed $callback): array
{
    $result = $arrays[0];
    foreach ($arrays[0] as $key => $value) {
        for ($index = 1, $count = \count($arrays); $index < $count; $index++) {
            if (
                \array_key_exists($key, $arrays[$index]) &&
                array_user_direct_compare_impl($callback, $value, $arrays[$index][$key]) === 0
            ) {
                unset($result[$key]);
                break;
            }
        }
    }
    return $result;
}

function array_user_intersect_direct_impl(array $arrays, mixed $callback): array
{
    $result = $arrays[0];
    foreach ($arrays[0] as $key => $value) {
        for ($index = 1, $count = \count($arrays); $index < $count; $index++) {
            if (
                !\array_key_exists($key, $arrays[$index]) ||
                array_user_direct_compare_impl($callback, $value, $arrays[$index][$key]) !== 0
            ) {
                unset($result[$key]);
                break;
            }
        }
    }
    return $result;
}

function array_user_diff_sorted_impl(
    array $arrays,
    string $behavior,
    ?\Closure $data_compare,
    ?\Closure $key_compare
): array {
    if ($data_compare === null) {
        $data_compare = static fn(mixed $first, mixed $second): int =>
            array_user_string_compare_impl($first, $second);
    }
    $sort_compare = $behavior === 'normal' ? $data_compare : $key_compare;
    $by_key = $behavior !== 'normal';
    $lists = [];
    $lengths = [];
    foreach ($arrays as $source) {
        $list = array_user_sorted_entries_impl($source, $sort_compare, $by_key);
        $lists[] = $list;
        $lengths[] = \count($list);
    }

    $pointers = \array_fill(0, \count($lists), 0);
    $result = $arrays[0];
    while ($pointers[0] < $lengths[0]) {
        $comparison = 1;
        $matched_index = 0;
        for ($index = 1, $count = \count($lists); $index < $count; $index++) {
            if ($behavior === 'normal') {
                while (
                    $pointers[$index] < $lengths[$index] &&
                    0 < ($comparison = $data_compare(
                        $lists[0][$pointers[0]][1],
                        $lists[$index][$pointers[$index]][1]
                    ))
                ) {
                    $pointers[$index]++;
                }
                $candidate = $pointers[$index];
            } else {
                $candidate = $pointers[$index];
                while (
                    $candidate < $lengths[$index] &&
                    0 !== ($comparison = $key_compare(
                        $lists[0][$pointers[0]][0],
                        $lists[$index][$candidate][0]
                    ))
                ) {
                    $candidate++;
                }
            }

            if ($comparison === 0) {
                if ($behavior === 'normal') {
                    if ($pointers[$index] < $lengths[$index]) {
                        $pointers[$index]++;
                    }
                    $matched_index = $index;
                    break;
                }
                if ($behavior === 'assoc') {
                    if ($candidate < $lengths[$index]) {
                        if ($data_compare(
                            $lists[0][$pointers[0]][1],
                            $lists[$index][$candidate][1]
                        ) !== 0) {
                            $comparison = -1;
                        } else {
                            $matched_index = $index;
                            break;
                        }
                    }
                } else {
                    $matched_index = $index;
                    break;
                }
            }
        }

        if ($comparison === 0) {
            do {
                $entry = $lists[0][$pointers[0]];
                unset($result[$entry[0]]);
                $pointers[0]++;
                if ($pointers[0] >= $lengths[0] || $behavior !== 'normal') {
                    break;
                }
            } while ($data_compare(
                $lists[0][$pointers[0] - 1][1],
                $lists[0][$pointers[0]][1]
            ) === 0);
        } else {
            do {
                $pointers[0]++;
                if ($pointers[0] >= $lengths[0] || $behavior !== 'normal') {
                    break;
                }
            } while ($data_compare(
                $lists[0][$pointers[0] - 1][1],
                $lists[0][$pointers[0]][1]
            ) === 0);
        }
    }
    return $result;
}

function array_user_intersect_sorted_impl(
    array $arrays,
    string $behavior,
    ?\Closure $data_compare,
    ?\Closure $key_compare
): array {
    if ($data_compare === null) {
        $data_compare = static fn(mixed $first, mixed $second): int =>
            array_user_string_compare_impl($first, $second);
    }
    $sort_compare = $behavior === 'normal' ? $data_compare : $key_compare;
    $by_key = $behavior !== 'normal';
    $lists = [];
    $lengths = [];
    foreach ($arrays as $source) {
        $list = array_user_sorted_entries_impl($source, $sort_compare, $by_key);
        $lists[] = $list;
        $lengths[] = \count($list);
    }

    $pointers = \array_fill(0, \count($lists), 0);
    $result = $arrays[0];
    $comparison = 0;
    while ($pointers[0] < $lengths[0]) {
        $mismatch_index = 0;
        for ($index = 1, $count = \count($lists); $index < $count; $index++) {
            if ($behavior === 'normal') {
                while (
                    $pointers[$index] < $lengths[$index] &&
                    0 < ($comparison = $data_compare(
                        $lists[0][$pointers[0]][1],
                        $lists[$index][$pointers[$index]][1]
                    ))
                ) {
                    $pointers[$index]++;
                }
            } else {
                while (
                    $pointers[$index] < $lengths[$index] &&
                    0 < ($comparison = $key_compare(
                        $lists[0][$pointers[0]][0],
                        $lists[$index][$pointers[$index]][0]
                    ))
                ) {
                    $pointers[$index]++;
                }
                if (
                    $comparison === 0 &&
                    $pointers[$index] < $lengths[$index] &&
                    $behavior === 'assoc' &&
                    $data_compare(
                        $lists[0][$pointers[0]][1],
                        $lists[$index][$pointers[$index]][1]
                    ) !== 0
                ) {
                    $comparison = 1;
                }
            }

            if ($pointers[$index] >= $lengths[$index]) {
                while ($pointers[0] < $lengths[0]) {
                    $entry = $lists[0][$pointers[0]++];
                    unset($result[$entry[0]]);
                }
                return $result;
            }
            if ($comparison !== 0) {
                $mismatch_index = $index;
                break;
            }
            $pointers[$index]++;
        }

        if ($comparison !== 0) {
            do {
                $entry = $lists[0][$pointers[0]];
                unset($result[$entry[0]]);
                $pointers[0]++;
                if ($pointers[0] >= $lengths[0] || $behavior !== 'normal') {
                    break;
                }
            } while ($data_compare(
                $lists[0][$pointers[0]][1],
                $lists[$mismatch_index][$pointers[$mismatch_index]][1]
            ) < 0);
        } else {
            do {
                $pointers[0]++;
                if ($pointers[0] >= $lengths[0] || $behavior !== 'normal') {
                    break;
                }
            } while ($data_compare(
                $lists[0][$pointers[0] - 1][1],
                $lists[0][$pointers[0]][1]
            ) === 0);
        }
    }
    return $result;
}

function base_convert_impl(string $num, int $from_base, int $to_base): string
{
    if ($from_base < 2 || $from_base > 36) {
        throw new \ValueError(
            'base_convert(): Argument #2 ($from_base) must be between 2 and 36 (inclusive)'
        );
    }
    if ($to_base < 2 || $to_base > 36) {
        throw new \ValueError(
            'base_convert(): Argument #3 ($to_base) must be between 2 and 36 (inclusive)'
        );
    }

    return base_encode_impl(base_parse_impl($num, $from_base), $to_base);
}

function bindec_impl(string $binary_string): int|float
{
    return base_parse_impl($binary_string, 2);
}

function hexdec_impl(string $hex_string): int|float
{
    return base_parse_impl($hex_string, 16);
}

function octdec_impl(string $octal_string): int|float
{
    return base_parse_impl($octal_string, 8);
}

function base_parse_impl(string $number, int $base): int|float
{
    $result = 0;
    $invalid = false;
    for ($index = 0, $length = \strlen($number); $index < $length; $index++) {
        $character = $number[$index];
        if ($index === 1 && $number[0] === '0' &&
            (($base === 2 && ($character === 'b' || $character === 'B')) ||
             ($base === 8 && ($character === 'o' || $character === 'O')) ||
             ($base === 16 && ($character === 'x' || $character === 'X')))) {
            continue;
        }

        $digit = base_digit_value_impl($character);
        if ($digit < 0 || $digit >= $base) {
            $invalid = true;
            continue;
        }
        $result = $result * $base + $digit;
    }

    if ($invalid) {
        \PHP2Go\Internal\deprecated(
            'Invalid characters passed for attempted conversion, these have been ignored'
        );
    }
    return $result;
}

function base_encode_impl(int|float $number, int $base): string
{
    if ($number == 0) {
        return '0';
    }

    $digits = '0123456789abcdefghijklmnopqrstuvwxyz';
    $result = '';
    while ($number >= 1) {
        if (\is_int($number)) {
            $digit = $number % $base;
            $number = \intdiv($number, $base);
        } else {
            $digit = (int) \fmod($number, $base);
            $number = \floor($number / $base);
        }
        $result = $digits[$digit] . $result;
    }
    return $result;
}

function base_digit_value_impl(string $character): int
{
    $byte = \ord($character);
    if ($byte >= 48 && $byte <= 57) {
        return $byte - 48;
    }
    if ($byte >= 65 && $byte <= 90) {
        return $byte - 55;
    }
    if ($byte >= 97 && $byte <= 122) {
        return $byte - 87;
    }
    return -1;
}

function count_chars_impl(string $string, int $mode = 0): array|string
{
    if ($mode < 0 || $mode > 4) {
        throw new \ValueError(
            'count_chars(): Argument #2 ($mode) must be between 0 and 4 (inclusive)'
        );
    }

    $counts = \array_fill(0, 256, 0);
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $byte = \ord($string[$index]);
        $counts[$byte]++;
    }

    if ($mode === 0) {
        return $counts;
    }

    if ($mode === 1 || $mode === 2) {
        $result = [];
        foreach ($counts as $byte => $count) {
            if (($mode === 1 && $count > 0) || ($mode === 2 && $count === 0)) {
                $result[$byte] = $count;
            }
        }
        return $result;
    }

    $result = '';
    foreach ($counts as $byte => $count) {
        if (($mode === 3 && $count > 0) || ($mode === 4 && $count === 0)) {
            $result .= \chr($byte);
        }
    }
    return $result;
}

function crypt_impl(mixed $string, mixed $salt): string
{
    return \PHP2Go\Internal\crypt((string) $string, (string) $salt);
}

function decbin_impl(int $num): string
{
    return \sprintf('%b', $num);
}

function dechex_impl(int $num): string
{
    return \sprintf('%x', $num);
}

function decoct_impl(int $num): string
{
    return \sprintf('%o', $num);
}

function doubleval_impl(mixed $value): float
{
    return \floatval($value);
}

function shell_utf8_width_impl(string $value, int $offset, int $length): int
{
    $first = \ord($value[$offset]);
    if ($first < 128) {
        return 1;
    }
    if ($first >= 194 && $first <= 223) {
        return $offset + 1 < $length &&
            utf8_is_trail_impl(\ord($value[$offset + 1])) ? 2 : 0;
    }
    if ($first >= 224 && $first <= 239) {
        if ($offset + 2 >= $length) {
            return 0;
        }
        $second = \ord($value[$offset + 1]);
        $third = \ord($value[$offset + 2]);
        if (!utf8_is_trail_impl($third)) {
            return 0;
        }
        if ($first === 224) {
            return $second >= 160 && $second <= 191 ? 3 : 0;
        }
        if ($first === 237) {
            return $second >= 128 && $second <= 159 ? 3 : 0;
        }
        return utf8_is_trail_impl($second) ? 3 : 0;
    }
    if ($first >= 240 && $first <= 247) {
        if ($offset + 3 >= $length) {
            return 0;
        }
        $second = \ord($value[$offset + 1]);
        if (
            !utf8_is_trail_impl(\ord($value[$offset + 2])) ||
            !utf8_is_trail_impl(\ord($value[$offset + 3]))
        ) {
            return 0;
        }
        if ($first === 240) {
            return $second >= 144 && $second <= 191 ? 4 : 0;
        }
        return utf8_is_trail_impl($second) ? 4 : 0;
    }
    if ($first >= 248 && $first <= 251) {
        if ($offset + 4 >= $length) {
            return 0;
        }
        $second = \ord($value[$offset + 1]);
        for ($at = 2; $at < 5; $at++) {
            if (!utf8_is_trail_impl(\ord($value[$offset + $at]))) {
                return 0;
            }
        }
        if ($first === 248) {
            return $second >= 136 && $second <= 191 ? 5 : 0;
        }
        return utf8_is_trail_impl($second) ? 5 : 0;
    }
    if ($first >= 252 && $first <= 253) {
        if ($offset + 5 >= $length) {
            return 0;
        }
        $second = \ord($value[$offset + 1]);
        for ($at = 2; $at < 6; $at++) {
            if (!utf8_is_trail_impl(\ord($value[$offset + $at]))) {
                return 0;
            }
        }
        if ($first === 252) {
            return $second >= 132 && $second <= 191 ? 6 : 0;
        }
        return utf8_is_trail_impl($second) ? 6 : 0;
    }
    return 0;
}

function shell_command_special_impl(int $byte): bool
{
    return $byte === 35 || $byte === 38 || $byte === 59 || $byte === 96 ||
        $byte === 124 || $byte === 42 || $byte === 63 || $byte === 126 ||
        $byte === 60 || $byte === 62 || $byte === 94 || $byte === 40 ||
        $byte === 41 || $byte === 91 || $byte === 93 || $byte === 123 ||
        $byte === 125 || $byte === 36 || $byte === 92 || $byte === 10;
}

function shell_validate_impl(string $value, string $function, string $argument): int
{
    if (\strpos($value, "\0") !== false) {
        throw new \ValueError(
            $function . '(): Argument #1 ($' . $argument . ') must not contain any null bytes'
        );
    }
    $length = \strlen($value);
    if ($length > 2097149) {
        throw new \ValueError(
            ($function === 'escapeshellarg' ? 'Argument' : 'Command') .
            ' exceeds the allowed length of 2097152 bytes'
        );
    }
    return $length;
}

function escapeshellarg_impl(string $arg): string
{
    $length = shell_validate_impl($arg, 'escapeshellarg', 'arg');
    $result = "'";
    for ($index = 0; $index < $length;) {
        $width = shell_utf8_width_impl($arg, $index, $length);
        if ($width === 0) {
            $index++;
            continue;
        }
        if ($width > 1) {
            $result .= \substr($arg, $index, $width);
            $index += $width;
            continue;
        }
        if ($arg[$index] === "'") {
            $result .= \chr(39) . \chr(92) . \chr(39) . \chr(39);
        } else {
            $result .= $arg[$index];
        }
        $index++;
    }
    $result .= "'";
    if (\strlen($result) > 2097153) {
        throw new \ValueError(
            'Escaped argument exceeds the allowed length of 2097152 bytes'
        );
    }
    return $result;
}

function escapeshellcmd_impl(string $command): string
{
    $length = shell_validate_impl($command, 'escapeshellcmd', 'command');
    if ($length === 0) {
        return '';
    }

    $result = '';
    $paired_quote = '';
    for ($index = 0; $index < $length;) {
        $width = shell_utf8_width_impl($command, $index, $length);
        if ($width === 0) {
            $index++;
            continue;
        }
        if ($width > 1) {
            $result .= \substr($command, $index, $width);
            $index += $width;
            continue;
        }

        $byte = \ord($command[$index]);
        if ($byte === 34 || $byte === 39) {
            $quote = $command[$index];
            $escape = false;
            if ($paired_quote === '') {
                $found = false;
                for ($probe = $index + 1; $probe < $length; $probe++) {
                    if ($command[$probe] === $quote) {
                        $found = true;
                        break;
                    }
                }
                if ($found) {
                    $paired_quote = $quote;
                } else {
                    $escape = true;
                }
            } elseif ($paired_quote === $quote) {
                $paired_quote = '';
            } else {
                $escape = true;
            }
            if ($escape) {
                $result .= "\\";
            }
            $result .= $quote;
        } else {
            if (shell_command_special_impl($byte)) {
                $result .= "\\";
            }
            $result .= $command[$index];
        }
        $index++;
    }

    if (\strlen($result) > 2097153) {
        throw new \ValueError(
            'Escaped command exceeds the allowed length of 2097152 bytes'
        );
    }
    return $result;
}

function join_impl(string|array $separator, ?array $array = null): string
{
    return $array === null ? \implode($separator) : \implode($separator, $array);
}

function key_exists_impl($key, array $array): bool
{
    return \array_key_exists($key, $array);
}

function strchr_impl(string $haystack, string $needle, bool $before_needle = false): string|false
{
    return \strstr($haystack, $needle, $before_needle);
}

function image_type_to_extension_impl(int $image_type, bool $include_dot = true): string|false
{
    $extensions = [
        1 => 'gif',
        2 => 'jpeg',
        3 => 'png',
        4 => 'swf',
        5 => 'psd',
        6 => 'bmp',
        7 => 'tiff',
        8 => 'tiff',
        9 => 'jpc',
        10 => 'jp2',
        11 => 'jpx',
        12 => 'jb2',
        13 => 'swf',
        14 => 'iff',
        15 => 'bmp',
        16 => 'xbm',
        17 => 'ico',
        18 => 'webp',
        19 => 'avif',
    ];
    if (!\array_key_exists($image_type, $extensions)) {
        return false;
    }
    return ($include_dot ? '.' : '') . $extensions[$image_type];
}

function image_type_to_mime_type_impl(int $image_type): string
{
    $mime_types = [
        1 => 'image/gif',
        2 => 'image/jpeg',
        3 => 'image/png',
        4 => 'application/x-shockwave-flash',
        5 => 'image/psd',
        6 => 'image/bmp',
        7 => 'image/tiff',
        8 => 'image/tiff',
        10 => 'image/jp2',
        13 => 'application/x-shockwave-flash',
        14 => 'image/iff',
        15 => 'image/vnd.wap.wbmp',
        16 => 'image/xbm',
        17 => 'image/vnd.microsoft.icon',
        18 => 'image/webp',
        19 => 'image/avif',
    ];
    return $mime_types[$image_type] ?? 'application/octet-stream';
}

function localeconv_impl(): array
{
    // setlocale() is not exposed by this runtime yet, so the only reachable
    // numeric and monetary locale is the linked engine's startup C locale.
    return [
        'decimal_point' => '.',
        'thousands_sep' => '',
        'int_curr_symbol' => '',
        'currency_symbol' => '',
        'mon_decimal_point' => '',
        'mon_thousands_sep' => '',
        'positive_sign' => '',
        'negative_sign' => '',
        'int_frac_digits' => 255,
        'frac_digits' => 255,
        'p_cs_precedes' => 255,
        'p_sep_by_space' => 255,
        'n_cs_precedes' => 255,
        'n_sep_by_space' => 255,
        'p_sign_posn' => 255,
        'n_sign_posn' => 255,
        'grouping' => [],
        'mon_grouping' => [],
    ];
}

function get_html_translation_table_impl(
    int $table = HTML_SPECIALCHARS,
    int $flags = ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401,
    string $encoding = 'UTF-8'
): array {
    if ($encoding === '') {
        $internal = \ini_get('internal_encoding');
        if ($internal !== false && $internal !== '') {
            $encoding = $internal;
        } else {
            $default = \ini_get('default_charset');
            $encoding = $default === false || $default === ''
                ? 'UTF-8'
                : $default;
        }
    }
    return \PHP2Go\Internal\html_translation_table(
        $table,
        $flags,
        $encoding
    );
}

function ob_get_status_impl(mixed $full_status = false): array
{
    $statuses = \PHP2Go\Internal\output_buffer_status();
    if ((bool) $full_status) {
        return $statuses;
    }
    $count = \count($statuses);
    return $count === 0 ? [] : $statuses[$count - 1];
}

function ob_list_handlers_impl(): array
{
    $handlers = [];
    foreach (\PHP2Go\Internal\output_buffer_status() as $status) {
        $handlers[] = $status['name'];
    }
    return $handlers;
}
function ob_implicit_flush_impl(mixed $enable = true): void
{
    // The embed runtime writes through an unbuffered output sink, and flush()
    // has no lower SAPI buffer to drain. PHP output buffers are deliberately
    // unaffected by implicit flushing, so there is no request state to keep.
}

function output_reset_rewrite_vars_impl(): bool
{
    return \PHP2Go\Internal\output_rewriter('reset');
}

function output_add_rewrite_var_impl(mixed $name, mixed $value): bool
{
    $name = (string) $name;
    $value = (string) $value;
    return \PHP2Go\Internal\output_rewriter(
        'add',
        \rawurlencode($name),
        \rawurlencode($value),
        \htmlspecialchars(
            $name,
            ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401,
            'UTF-8',
            false
        ),
        \htmlspecialchars(
            $value,
            ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401,
            'UTF-8',
            false
        )
    );
}

function nl_langinfo_impl(mixed $item): string|false
{
    // LC_TIME, LC_NUMERIC, LC_MONETARY and LC_MESSAGES start in C; LC_CTYPE
    // starts in C.UTF-8. A future setlocale() implementation must route these
    // values through the same modeled locale state rather than this fixed map.
    $item = (int) $item;
    if ($item >= 131072 && $item <= 131078) {
        return ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'][
            $item - 131072
        ];
    }
    if ($item >= 131079 && $item <= 131085) {
        return [
            'Sunday', 'Monday', 'Tuesday', 'Wednesday',
            'Thursday', 'Friday', 'Saturday',
        ][$item - 131079];
    }
    if ($item >= 131086 && $item <= 131097) {
        return [
            'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
            'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
        ][$item - 131086];
    }
    if ($item >= 131098 && $item <= 131109) {
        return [
            'January', 'February', 'March', 'April', 'May', 'June',
            'July', 'August', 'September', 'October', 'November', 'December',
        ][$item - 131098];
    }

    $fixed = [
        14 => 'UTF-8',
        65536 => '.',
        65537 => '',
        131110 => 'AM',
        131111 => 'PM',
        131112 => '%a %b %e %H:%M:%S %Y',
        131113 => '%m/%d/%y',
        131114 => '%H:%M:%S',
        131115 => '%I:%M:%S %p',
        262159 => '-',
        327680 => '^[yY]',
        327681 => '^[nN]',
        327682 => '',
        327683 => '',
    ];
    if (\array_key_exists($item, $fixed)) {
        return $fixed[$item];
    }
    if (($item >= 131116 && $item <= 131121) ||
        ($item >= 262144 && $item <= 262150)) {
        return '';
    }
    if ($item >= 262151 && $item <= 262158) {
        return locale_byte_impl(255);
    }

    \PHP2Go\Internal\warning(
        "nl_langinfo(): Item '" . $item . "' is not valid"
    );
    return false;
}

function locale_byte_impl(int $byte): string
{
    // Keeping byte construction behind a parameter prevents the harvester
    // from folding 0xff into a protobuf string constant, which must be UTF-8.
    return \chr($byte);
}

function clearstatcache_impl(
    mixed $clear_realpath_cache = false,
    mixed $filename = ''
): void {
    // php2go performs every modeled file lookup directly and has no stat or
    // metadata cache to invalidate. The native path leg does retain Zend's
    // separately observable realpath cache.
    if ($clear_realpath_cache) {
        \PHP2Go\Internal\realpath_cache(
            'clear',
            (string) $filename,
            \func_num_args() >= 2
        );
    }
}

function memory_reset_peak_usage_impl(): void
{
    // Current and peak memory are the same fixed modeled value, so resetting
    // the peak has no additional state to change.
}

function gethostname_impl(): string|false
{
    return \php_uname('n');
}

function sys_get_temp_dir_impl(): string
{
    static $directory = null;
    if ($directory !== null) {
        return $directory;
    }
    $directory = \getenv('TMPDIR');
    if ($directory === false || $directory === '') {
        $directory = '/tmp';
    } elseif ($directory[\strlen($directory) - 1] === DIRECTORY_SEPARATOR) {
        $directory = \substr($directory, 0, -1);
    }
    return $directory;
}

function dl_impl(mixed $extension_filename): bool
{
    $extension_filename = (string) $extension_filename;
    $nul = \strpos($extension_filename, "\0");
    if ($nul !== false) {
        $extension_filename = \substr($extension_filename, 0, $nul);
    }
    \PHP2Go\Internal\warning(
        'dl(): Cannot dynamically load ' . $extension_filename .
        ' - dynamic modules are not supported'
    );
    return false;
}

function http_get_last_response_headers_impl(): ?array
{
    // No HTTP stream wrapper exists, so no response can publish this state.
    return null;
}

function http_clear_last_response_headers_impl(): void
{
}

function request_parse_body_impl(mixed $options = null): array
{
    if ($options !== null) {
        foreach ($options as $key => $value) {
            if (\is_int($key)) {
                throw new \ValueError(
                    'Invalid integer key in $options argument'
                );
            }
            if ($key === '') {
                throw new \ValueError(
                    'Invalid empty string key in $options argument'
                );
            }

            $normalized = \strtolower($key);
            if (
                $normalized !== 'max_file_uploads' &&
                $normalized !== 'max_input_vars' &&
                $normalized !== 'max_multipart_body_parts' &&
                $normalized !== 'post_max_size' &&
                $normalized !== 'upload_max_filesize'
            ) {
                throw new \ValueError(
                    'Invalid key "' . $key . '" in $options argument'
                );
            }

            if (!\is_int($value) && !\is_string($value)) {
                if ($value === null) {
                    $type = 'null';
                } elseif ($value === true) {
                    $type = 'true';
                } elseif ($value === false) {
                    $type = 'false';
                } elseif (\is_resource($value)) {
                    $type = 'resource';
                } else {
                    $type = \get_debug_type($value);
                }
                throw new \ValueError(
                    'Invalid ' . $type . ' value in $options argument'
                );
            }

            if (\is_string($value)) {
                // The request parser uses zend_ini_parse_quantity() for the
                // same warning and overflow contract as this public facade.
                \ini_parse_quantity($value);
            }
        }
    }

    // php_embed_init() installs neither request Content-Type nor input bytes.
    // PHP validates the option cache first, then raises this fixed exception.
    throw new \RequestParseBodyException(
        'Request does not provide a content type'
    );
}

function is_uploaded_file_impl(mixed $filename): bool
{
    $filename = (string) $filename;
    if (\strpos($filename, "\0") !== false) {
        throw new \ValueError(
            'is_uploaded_file(): Argument #1 ($filename) ' .
            'must not contain any null bytes'
        );
    }
    return false;
}

function move_uploaded_file_impl(mixed $from, mixed $to): bool
{
    $to = (string) $to;
    if (\strpos($to, "\0") !== false) {
        throw new \ValueError(
            'move_uploaded_file(): Argument #2 ($to) ' .
            'must not contain any null bytes'
        );
    }
    return false;
}

function clock_wall_impl(): array
{
    return \PHP2Go\Internal\clock('wall');
}

function gettimeofday_impl(mixed $as_float = false): array|float
{
    $wall = clock_wall_impl();
    if ((bool) $as_float) {
        return $wall[0] + ($wall[1] / 1000000);
    }
    return [
        'sec' => $wall[0],
        'usec' => $wall[1],
        'minuteswest' => 0,
        'dsttime' => 0,
    ];
}

function microtime_impl(mixed $as_float = false): string|float
{
    $wall = clock_wall_impl();
    if ((bool) $as_float) {
        return $wall[0] + ($wall[1] / 1000000);
    }
    $fraction = (string) $wall[1];
    while (\strlen($fraction) < 6) {
        $fraction = '0' . $fraction;
    }
    return '0.' . $fraction . '00 ' . $wall[0];
}

function hrtime_impl(mixed $as_number = false): array|int|float|false
{
    $nanoseconds = \PHP2Go\Internal\clock('monotonic');
    if ((bool) $as_number) {
        return $nanoseconds;
    }
    return [
        \intdiv($nanoseconds, 1000000000),
        $nanoseconds % 1000000000,
    ];
}

function sleep_impl(mixed $seconds): int
{
    $seconds = (int) $seconds;
    if ($seconds < 0) {
        throw new \ValueError(
            'sleep(): Argument #1 ($seconds) must be greater than or equal to 0'
        );
    }
    \PHP2Go\Internal\clock('sleep', $seconds, 0);
    return 0;
}

function usleep_impl(mixed $microseconds): void
{
    $microseconds = (int) $microseconds;
    if ($microseconds < 0) {
        throw new \ValueError(
            'usleep(): Argument #1 ($microseconds) must be greater than or equal to 0'
        );
    }
    \PHP2Go\Internal\clock(
        'sleep',
        \intdiv($microseconds, 1000000),
        ($microseconds % 1000000) * 1000
    );
}

function time_nanosleep_impl(
    mixed $seconds,
    mixed $nanoseconds
): array|bool {
    $seconds = (int) $seconds;
    $nanoseconds = (int) $nanoseconds;
    if ($seconds < 0) {
        throw new \ValueError(
            'time_nanosleep(): Argument #1 ($seconds) ' .
            'must be greater than or equal to 0'
        );
    }
    if ($nanoseconds < 0) {
        throw new \ValueError(
            'time_nanosleep(): Argument #2 ($nanoseconds) ' .
            'must be greater than or equal to 0'
        );
    }
    if ($nanoseconds > 999999999) {
        throw new \ValueError(
            'Nanoseconds was not in the range 0 to 999 999 999 ' .
            'or seconds was negative'
        );
    }
    \PHP2Go\Internal\clock('sleep', $seconds, $nanoseconds);
    return true;
}

function time_sleep_until_impl(mixed $timestamp): bool
{
    $timestamp = (float) $timestamp;
    if (
        $timestamp !== $timestamp ||
        $timestamp < 0 ||
        $timestamp > 18446744073
    ) {
        throw new \ValueError(
            'time_sleep_until(): Argument #1 ($timestamp) ' .
            'must be between 0 and 18446744073'
        );
    }
    $wall = clock_wall_impl();
    $whole = (int) $timestamp;
    $nanoseconds = (int) (($timestamp - $whole) * 1000000000);
    $seconds = $whole - $wall[0];
    $nanoseconds -= $wall[1] * 1000;
    if ($nanoseconds < 0) {
        $seconds--;
        $nanoseconds += 1000000000;
    }
    if ($seconds < 0) {
        \PHP2Go\Internal\warning(
            'time_sleep_until(): Argument #1 ($timestamp) ' .
            'must be greater than or equal to the current time'
        );
        return false;
    }
    \PHP2Go\Internal\clock('sleep', $seconds, $nanoseconds);
    return true;
}

function strptime_impl(mixed $timestamp, mixed $format): array|false
{
    $parsed = \PHP2Go\Internal\strptime(
        (string) $timestamp,
        (string) $format
    );
    if ($parsed === false) {
        return false;
    }
    return [
        'tm_sec' => $parsed[0],
        'tm_min' => $parsed[1],
        'tm_hour' => $parsed[2],
        'tm_mday' => $parsed[3],
        'tm_mon' => $parsed[4],
        'tm_year' => $parsed[5],
        'tm_wday' => $parsed[6],
        'tm_yday' => $parsed[7],
        'unparsed' => $parsed[8],
    ];
}

function uniqid_impl(
    mixed $prefix = "",
    mixed $more_entropy = false
): string {
    $prefix = (string) $prefix;
    $nul = \strpos($prefix, "\0");
    if ($nul !== false) {
        $prefix = \substr($prefix, 0, $nul);
    }
    $more_entropy = (bool) $more_entropy;
    $parts = \PHP2Go\Internal\unique_id($more_entropy);
    $result = $prefix . \sprintf("%08x%05x", $parts[0], $parts[1]);
    if ($more_entropy) {
        $result .= \sprintf("%.8F", ($parts[2] / 4294967295) * 10);
    }
    return $result;
}

function get_current_user_impl(): string
{
    // The embed SAPI never installs a translated request script path.
    return '';
}

function getlastmod_impl(): int|false
{
    return false;
}

function getmyinode_impl(): int|false
{
    return false;
}

function getmypid_impl(): int|false
{
    return \PHP2Go\Internal\process_identity('pid');
}

function getmyuid_impl(): int|false
{
    return \PHP2Go\Internal\process_identity('uid');
}

function getmygid_impl(): int|false
{
    return \PHP2Go\Internal\process_identity('gid');
}

function connection_aborted_impl(): int
{
    // The embed SAPI has no client disconnect channel.
    return 0;
}

function connection_status_impl(): int
{
    return 0;
}

function ignore_user_abort_impl(?bool $enable = null): int
{
    static $ignored = false;
    $previous = $ignored ? 1 : 0;
    if ($enable !== null) {
        $ignored = $enable;
    }
    return $previous;
}

function php_ini_loaded_file_impl(): string|false
{
    // Bundled requests do not load a host php.ini.
    return false;
}

function php_ini_scanned_files_impl(): string|false
{
    return false;
}

function getopt_impl(
    mixed $short_options,
    array $long_options = [],
    mixed &$rest_index = null
): array|false {
    // php_embed_init() supplies only argv[0], so option scanning starts and
    // finishes at index 1. The native implementation still coerces every
    // long-option descriptor before it returns.
    $rest_index = 1;
    $short_options = (string) $short_options;
    foreach ($long_options as $option) {
        $option = (string) $option;
    }
    return [];
}

function get_browser_impl(
    mixed $user_agent = null,
    mixed $return_array = false
): object|array|false {
    \PHP2Go\Internal\warning('get_browser(): browscap ini directive not set');
    return false;
}

function get_cfg_var_impl(mixed $option): string|array|false
{
    $values = [
        'html_errors' => '0',
        'implicit_flush' => '1',
        'max_execution_time' => '0',
        'max_input_time' => '-1',
        'output_buffering' => '0',
        'register_argc_argv' => '1',
    ];
    return $values[(string) $option] ?? false;
}

function credits_cell_impl(string $value): string
{
    return \htmlspecialchars(
        $value,
        ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401,
        'UTF-8'
    );
}

function credits_rows_impl(string $data): array
{
    $rows = [];
    foreach (\explode("\n", $data) as $line) {
        $rows[] = \explode("\t", $line, 2);
    }
    return $rows;
}

function credits_single_table_impl(string $title, string $value): void
{
    echo "<table>\n";
    echo '<tr class="h"><th>', $title, "</th></tr>\n";
    echo '<tr><td class="e">', credits_cell_impl($value), " </td></tr>\n";
    echo "</table>\n";
}

function credits_table_impl(
    string $title,
    array $rows,
    ?array $columns = null
): void {
    echo "<table>\n";
    echo '<tr class="h"><th colspan="2">', $title, "</th></tr>\n";
    if ($columns !== null) {
        echo '<tr class="h"><th>', $columns[0], '</th><th>',
            $columns[1], "</th></tr>\n";
    }
    foreach ($rows as $row) {
        echo '<tr><td class="e">', credits_cell_impl($row[0]),
            ' </td><td class="v">', credits_cell_impl($row[1]),
            " </td></tr>\n";
    }
    echo "</table>\n";
}

function credits_html_head_impl(): void
{
    echo "<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Transitional//EN\" \"DTD/xhtml1-transitional.dtd\">\n";
    echo "<html xmlns=\"http://www.w3.org/1999/xhtml\"><head>\n";
    echo "<style type=\"text/css\">\n";
    echo "body {background-color: #fff; color: #222; font-family: sans-serif;}\n";
    echo "pre {margin: 0; font-family: monospace;}\n";
    echo "a {color: inherit;}\n";
    echo "a:hover {text-decoration: none;}\n";
    echo "table {border-collapse: collapse; border: 0; width: 934px; box-shadow: 1px 2px 3px rgba(0, 0, 0, 0.2);}\n";
    echo ".center {text-align: center;}\n";
    echo ".center table {margin: 1em auto; text-align: left;}\n";
    echo ".center th {text-align: center !important;}\n";
    echo "td, th {border: 1px solid #666; font-size: 75%; vertical-align: baseline; padding: 4px 5px;}\n";
    echo "th {position: sticky; top: 0; background: inherit;}\n";
    echo "h1 {font-size: 150%;}\n";
    echo "h2 {font-size: 125%;}\n";
    echo "h2 > a {text-decoration: none;}\n";
    echo "h2 > a:hover {text-decoration: underline;}\n";
    echo ".p {text-align: left;}\n";
    echo ".e {background-color: #ccf; width: 300px; font-weight: bold;}\n";
    echo ".h {background-color: #99c; font-weight: bold;}\n";
    echo ".v {background-color: #ddd; max-width: 300px; overflow-x: auto; word-wrap: break-word;}\n";
    echo ".v i {color: #999;}\n";
    echo "img {float: right; border: 0;}\n";
    echo "hr {width: 934px; background-color: #ccc; border: 0; height: 1px;}\n";
    echo ":root {--php-dark-grey: #333; --php-dark-blue: #4F5B93; --php-medium-blue: #8892BF; --php-light-blue: #E2E4EF; --php-accent-purple: #793862}";
    echo "@media (prefers-color-scheme: dark) {\n";
    echo "  body {background: var(--php-dark-grey); color: var(--php-light-blue)}\n";
    echo "  .h td, td.e, th {border-color: #606A90}\n";
    echo "  td {border-color: #505153}\n";
    echo "  .e {background-color: #404A77}\n";
    echo "  .h {background-color: var(--php-dark-blue)}\n";
    echo "  .v {background-color: var(--php-dark-grey)}\n";
    echo "  hr {background-color: #505153}\n";
    echo "}\n</style>\n";
    echo '<title>PHP ', PHP_VERSION, " - phpinfo()</title>";
    echo '<meta name="ROBOTS" content="NOINDEX,NOFOLLOW,NOARCHIVE" /></head>',
        "\n<body><div class=\"center\">\n";
}

function credits_general_rows_impl(): array
{
    return credits_rows_impl(
        "Zend Scripting Language Engine\tAndi Gutmans, Zeev Suraski, Stanislav Malyshev, Marcus Boerger, Dmitry Stogov, Xinchen Hui, Nikita Popov\n" .
        "Extension Module API\tAndi Gutmans, Zeev Suraski, Andrei Zmievski\n" .
        "UNIX Build and Modularization\tStig Bakken, Sascha Schumann, Jani Taskinen, Peter Kokot\n" .
        "Windows Support\tShane Caraveo, Zeev Suraski, Wez Furlong, Pierre-Alain Joye, Anatol Belski, Kalle Sommer Nielsen\n" .
        "Server API (SAPI) Abstraction Layer\tAndi Gutmans, Shane Caraveo, Zeev Suraski\n" .
        "Streams Abstraction Layer\tWez Furlong, Sara Golemon\n" .
        "PHP Data Objects Layer\tWez Furlong, Marcus Boerger, Sterling Hughes, George Schlossnagle, Ilia Alshanetsky\n" .
        "Output Handler\tZeev Suraski, Thies C. Arntzen, Marcus Boerger, Michael Wallner\n" .
        "Consistent 64 bit support\tAnthony Ferrara, Anatol Belski"
    );
}

function credits_sapi_rows_impl(): array
{
    return credits_rows_impl(
        "Apache 2 Handler\tIan Holsman, Justin Erenkrantz (based on Apache 2 Filter code)\n" .
        "CGI / FastCGI\tRasmus Lerdorf, Stig Bakken, Shane Caraveo, Dmitry Stogov\n" .
        "CLI\tEdin Kadribasic, Marcus Boerger, Johannes Schlueter, Moriyoshi Koizumi, Xinchen Hui\n" .
        "Embed\tEdin Kadribasic\n" .
        "FastCGI Process Manager\tAndrei Nigmatulin, dreamcat4, Antony Dovgal, Jerome Loyet\n" .
        "litespeed\tGeorge Wang\n" .
        "phpdbg\tFelipe Pena, Joe Watkins, Bob Weinand"
    );
}

function credits_module_rows_impl(): array
{
    return credits_rows_impl(
        "BC Math\tAndi Gutmans\n" .
        "Bzip2\tSterling Hughes\n" .
        "Calendar\tShane Caraveo, Colin Viebrock, Hartmut Holzgraefe, Wez Furlong\n" .
        "COM and .Net\tWez Furlong\n" .
        "ctype\tHartmut Holzgraefe\n" .
        "cURL\tSterling Hughes\n" .
        "Date/Time Support\tDerick Rethans\n" .
        "DB-LIB (MS SQL, Sybase)\tWez Furlong, Frank M. Kromann, Adam Baratz\n" .
        "DBA\tSascha Schumann, Marcus Boerger\n" .
        "DOM\tChristian Stocker, Rob Richards, Marcus Boerger, Nora Dossche\n" .
        "enchant\tPierre-Alain Joye, Ilia Alshanetsky\n" .
        "EXIF\tRasmus Lerdorf, Marcus Boerger\n" .
        "FFI\tDmitry Stogov\n" .
        "fileinfo\tIlia Alshanetsky, Pierre Alain Joye, Scott MacVicar, Derick Rethans, Anatol Belski\n" .
        "Firebird driver for PDO\tArd Biesheuvel\n" .
        "FTP\tStefan Esser, Andrew Skalski\n" .
        "GD imaging\tRasmus Lerdorf, Stig Bakken, Jim Winstead, Jouni Ahto, Ilia Alshanetsky, Pierre-Alain Joye, Marcus Boerger, Mark Randall\n" .
        "GetText\tAlex Plotnick\n" .
        "GNU GMP support\tStanislav Malyshev\n" .
        "Iconv\tRui Hirokawa, Stig Bakken, Moriyoshi Koizumi\n" .
        "Input Filter\tRasmus Lerdorf, Derick Rethans, Pierre-Alain Joye, Ilia Alshanetsky\n" .
        "Internationalization\tEd Batutis, Vladimir Iordanov, Dmitry Lakhtyuk, Stanislav Malyshev, Vadim Savchuk, Kirti Velankar\n" .
        "JSON\tJakub Zelenka, Omar Kilani, Scott MacVicar\n" .
        "LDAP\tAmitay Isaacs, Eric Warnke, Rasmus Lerdorf, Gerrit Thomson, Stig Venaas\n" .
        "LIBXML\tChristian Stocker, Rob Richards, Marcus Boerger, Wez Furlong, Shane Caraveo\n" .
        "Multibyte String Functions\tTsukada Takuya, Rui Hirokawa\n" .
        "MySQL driver for PDO\tGeorge Schlossnagle, Wez Furlong, Ilia Alshanetsky, Johannes Schlueter\n" .
        "MySQLi\tZak Greant, Georg Richter, Andrey Hristov, Ulf Wendel\n" .
        "MySQLnd\tAndrey Hristov, Ulf Wendel, Georg Richter, Johannes Schlüter\n" .
        "ODBC driver for PDO\tWez Furlong\n" .
        "ODBC\tStig Bakken, Andreas Karajannis, Frank M. Kromann, Daniel R. Kalowsky\n" .
        "Opcache\tAndi Gutmans, Zeev Suraski, Stanislav Malyshev, Dmitry Stogov, Xinchen Hui\n" .
        "OpenSSL\tStig Venaas, Wez Furlong, Sascha Kettler, Scott MacVicar, Eliot Lear\n" .
        "pcntl\tJason Greene, Arnaud Le Blanc\n" .
        "Perl Compatible Regexps\tAndrei Zmievski\n" .
        "PHP Archive\tGregory Beaver, Marcus Boerger\n" .
        "PHP Data Objects\tWez Furlong, Marcus Boerger, Sterling Hughes, George Schlossnagle, Ilia Alshanetsky\n" .
        "PHP hash\tSara Golemon, Rasmus Lerdorf, Stefan Esser, Michael Wallner, Scott MacVicar\n" .
        "Posix\tKristian Koehntopp\n" .
        "PostgreSQL driver for PDO\tEdin Kadribasic, Ilia Alshanetsky\n" .
        "PostgreSQL\tJouni Ahto, Zeev Suraski, Yasuo Ohgaki, Chris Kings-Lynne\n" .
        "random\tGo Kudo, Tim Düsterhus, Guilliam Xavier, Christoph M. Becker, Jakub Zelenka, Bob Weinand, Máté Kocsis, and Original RNG implementators\n" .
        "Readline\tThies C. Arntzen\n" .
        "Reflection\tMarcus Boerger, Timm Friebe, George Schlossnagle, Andrei Zmievski, Johannes Schlueter\n" .
        "Sessions\tSascha Schumann, Andrei Zmievski\n" .
        "Shared Memory Operations\tSlava Poliakov, Ilia Alshanetsky\n" .
        "SimpleXML\tSterling Hughes, Marcus Boerger, Rob Richards\n" .
        "SNMP\tRasmus Lerdorf, Harrie Hazewinkel, Mike Jackson, Steven Lawrance, Johann Hanne, Boris Lytochkin\n" .
        "SOAP\tBrad Lafountain, Shane Caraveo, Dmitry Stogov\n" .
        "Sockets\tChris Vandomelen, Sterling Hughes, Daniel Beulshausen, Jason Greene\n" .
        "Sodium\tFrank Denis\n" .
        "SPL\tMarcus Boerger, Etienne Kneuss\n" .
        "SQLite 3.x driver for PDO\tWez Furlong\n" .
        "SQLite3\tScott MacVicar, Ilia Alshanetsky, Brad Dewar\n" .
        "System V Message based IPC\tWez Furlong\n" .
        "System V Semaphores\tTom May\n" .
        "System V Shared Memory\tChristian Cartus\n" .
        "tidy\tJohn Coggeshall, Ilia Alshanetsky\n" .
        "tokenizer\tAndrei Zmievski, Johannes Schlueter\n" .
        "XML\tStig Bakken, Thies C. Arntzen, Sterling Hughes\n" .
        "XMLReader\tRob Richards\n" .
        "XMLWriter\tRob Richards, Pierre-Alain Joye\n" .
        "XSL\tChristian Stocker, Rob Richards\n" .
        "Zip\tPierre-Alain Joye, Remi Collet\n" .
        "Zlib\tRasmus Lerdorf, Stefan Roehrich, Zeev Suraski, Jade Nicoletti, Michael Wallner"
    );
}

function phpcredits_impl(mixed $flags = CREDITS_ALL): true
{
    $flags = (int) $flags;
    $flags &= 4294967295;
    if (($flags & CREDITS_FULLPAGE) !== 0) {
        credits_html_head_impl();
    }
    echo "<h1>PHP Credits</h1>\n";
    if (($flags & CREDITS_GROUP) !== 0) {
        credits_single_table_impl(
            'PHP Group',
            'Thies C. Arntzen, Stig Bakken, Shane Caraveo, Andi Gutmans, Rasmus Lerdorf, Sam Ruby, Sascha Schumann, Zeev Suraski, Jim Winstead, Andrei Zmievski'
        );
    }
    if (($flags & CREDITS_GENERAL) !== 0) {
        credits_single_table_impl(
            'Language Design &amp; Concept',
            'Andi Gutmans, Rasmus Lerdorf, Zeev Suraski, Marcus Boerger'
        );
        credits_table_impl('PHP Authors', credits_general_rows_impl(), [
            'Contribution',
            'Authors',
        ]);
    }
    if (($flags & CREDITS_SAPI) !== 0) {
        credits_table_impl('SAPI Modules', credits_sapi_rows_impl(), [
            'Contribution',
            'Authors',
        ]);
    }
    if (($flags & CREDITS_MODULES) !== 0) {
        credits_table_impl('Module Authors', credits_module_rows_impl(), [
            'Module',
            'Authors',
        ]);
    }
    if (($flags & CREDITS_DOCS) !== 0) {
        credits_table_impl(
            'PHP Documentation',
            credits_rows_impl(
                "Authors\tMehdi Achour, Friedhelm Betz, Antony Dovgal, Nuno Lopes, Hannes Magnusson, Philip Olson, Georg Richter, Damien Seguy, Jakub Vrana, Adam Harvey\n" .
                "Editor\tPeter Cowburn\n" .
                "User Note Maintainers\tDaniel P. Brown, Thiago Henrique Pojda\n" .
                "Other Contributors\tPreviously active authors, editors and other contributors are listed in the manual."
            )
        );
    }
    if (($flags & CREDITS_QA) !== 0) {
        credits_single_table_impl(
            'PHP Quality Assurance Team',
            'Ilia Alshanetsky, Joerg Behrens, Antony Dovgal, Stefan Esser, Moriyoshi Koizumi, Magnus Maatta, Sebastian Nohn, Derick Rethans, Melvyn Sopacua, Pierre-Alain Joye, Dmitry Stogov, Felipe Pena, David Soria Parra, Stanislav Malyshev, Julien Pauli, Stephen Zarkos, Anatol Belski, Remi Collet, Ferenc Kovacs'
        );
    }
    if (($flags & 128) !== 0) {
        credits_table_impl(
            'Websites and Infrastructure team',
            credits_rows_impl(
                "PHP Websites Team\tRasmus Lerdorf, Hannes Magnusson, Philip Olson, Lukas Kahwe Smith, Pierre-Alain Joye, Kalle Sommer Nielsen, Peter Cowburn, Adam Harvey, Ferenc Kovacs, Levi Morrison\n" .
                "Event Maintainers\tDamien Seguy, Daniel P. Brown\n" .
                "Network Infrastructure\tDaniel P. Brown\n" .
                "Windows Infrastructure\tAlex Schoenmaker"
            )
        );
    }
    if (($flags & CREDITS_FULLPAGE) !== 0) {
        echo "</div></body></html>\n";
    }
    return true;
}

function phpinfo_cell_impl(mixed $value, bool $ini = false): string
{
    if ($value === null) {
        return '<i>no value</i>';
    }
    if ($ini && $value === '1') {
        return 'On';
    }
    if ($ini && $value === '') {
        return 'Off';
    }
    if ($value === true) {
        return '1';
    }
    if ($value === false) {
        return '';
    }
    if (is_array($value)) {
        return 'Array';
    }
    if (is_object($value)) {
        return 'Object';
    }
    return credits_cell_impl((string) $value);
}

function phpinfo_rows_impl(array $rows): void
{
    echo "<table>\n";
    foreach ($rows as $row) {
        echo '<tr><td class="e">', phpinfo_cell_impl($row[0]),
            ' </td><td class="v">', phpinfo_cell_impl($row[1]),
            " </td></tr>\n";
    }
    echo "</table>\n";
}

function phpinfo_ini_value_impl(string $name, mixed $value): string
{
    $rendered = phpinfo_cell_impl($value, true);
    if ($value !== null && \str_starts_with($name, 'highlight.')) {
        return '<span style="color: ' . credits_cell_impl((string) $value) . '">' .
            credits_cell_impl((string) $value) . '</span>';
    }
    return $rendered;
}

function phpinfo_ini_table_impl(?string $extension): void
{
    $entries = \ini_get_all($extension, true);
    if (!\is_array($entries) || $entries === []) {
        return;
    }
    echo "<table>\n";
    echo '<tr class="h"><th>Directive</th><th>Local Value</th>',
        "<th>Master Value</th></tr>\n";
    foreach ($entries as $name => $entry) {
        echo '<tr><td class="e">', credits_cell_impl((string) $name),
            '</td><td class="v">',
            phpinfo_ini_value_impl((string) $name, $entry['local_value']),
            '</td><td class="v">',
            phpinfo_ini_value_impl((string) $name, $entry['global_value']),
            "</td></tr>\n";
    }
    echo "</table>\n";
}

function phpinfo_module_heading_impl(string $name): void
{
    $anchor = \strtolower($name);
    echo '<h2><a name="module_', $anchor, '" href="#module_', $anchor,
        '">', $name, "</a></h2>\n";
}

function phpinfo_general_impl(): void
{
    echo '<h1 class="p">PHP Version ', PHP_VERSION, "</h1>\n";
    phpinfo_rows_impl([
        ['System', \php_uname('a')],
        ['Server API', 'Embed'],
        ['Virtual Directory Support', 'disabled'],
        ['Configuration File (php.ini) Path', '/home/moriyoshi/src/php2go/third_party/php-src/lib'],
        ['Loaded Configuration File', '(none)'],
        ['Scan this dir for additional .ini files', '(none)'],
        ['Additional .ini files parsed', '(none)'],
        ['PHP Integer Size', (string) (PHP_INT_SIZE * 8) . ' bits'],
        ['Debug Build', 'no'],
        ['Thread Safety', 'disabled'],
        ['Zend Signal Handling', 'disabled'],
        ['Zend Memory Manager', 'enabled'],
        ['Zend Multibyte Support', 'disabled'],
        ['Zend Max Execution Timers', 'disabled'],
        ['IPv6 Support', 'enabled'],
        ['DTrace Support', 'disabled'],
        ['PHP Streams', \implode(', ', \stream_get_wrappers())],
        ['Stream Socket Transports', \implode(', ', \stream_get_transports())],
        ['Stream Filters', \implode(', ', \stream_get_filters())],
    ]);
    echo "<table>\n<tr class=\"v\"><td>";
    echo 'This program makes use of the Zend Scripting Language Engine:<br />';
    echo 'Zend Engine v', PHP_VERSION;
    echo "</td></tr>\n</table>\n";
}

function phpinfo_modules_impl(): void
{
    phpinfo_module_heading_impl('Core');
    phpinfo_rows_impl([['PHP Version', PHP_VERSION]]);
    phpinfo_ini_table_impl(null);

    phpinfo_module_heading_impl('date');
    phpinfo_rows_impl([
        ['date/time support', 'enabled'],
        ['Timezone Database', 'internal'],
        ['Default timezone', (string) \ini_get('date.timezone')],
    ]);
    phpinfo_ini_table_impl('date');

    phpinfo_module_heading_impl('hash');
    phpinfo_rows_impl([
        ['hash support', 'enabled'],
        ['Hashing Engines', 'md2 md4 md5 sha1 sha224 sha256 sha384 sha512'],
    ]);

    phpinfo_module_heading_impl('json');
    phpinfo_rows_impl([['json support', 'enabled']]);

    phpinfo_module_heading_impl('pcre');
    phpinfo_rows_impl([
        ['PCRE (Perl Compatible Regular Expressions) Support', 'enabled'],
        ['PCRE Library Version', \defined('PCRE_VERSION') ? PCRE_VERSION : '10.44 2024-06-07'],
        ['PCRE JIT Support', 'enabled'],
    ]);
    phpinfo_ini_table_impl('pcre');

    phpinfo_module_heading_impl('random');
    phpinfo_rows_impl([['Version', PHP_VERSION]]);
    phpinfo_module_heading_impl('Reflection');
    phpinfo_rows_impl([['Reflection', 'enabled']]);
    phpinfo_module_heading_impl('SPL');
    phpinfo_rows_impl([['SPL support', 'enabled']]);

    phpinfo_module_heading_impl('standard');
    phpinfo_rows_impl([
        ['Dynamic Library Support', 'unavailable'],
        ['Path to sendmail', (string) \ini_get('sendmail_path')],
    ]);
    phpinfo_ini_table_impl('standard');

    echo "<h2>Additional Modules</h2>\n<table>\n";
    echo "<tr class=\"h\"><th>Module Name</th></tr>\n</table>\n";
}

function phpinfo_environment_impl(): void
{
    echo "<h2>Environment</h2>\n<table>\n";
    echo "<tr class=\"h\"><th>Variable</th><th>Value</th></tr>\n";
    $environment = \getenv();
    if (\is_array($environment)) {
        foreach ($environment as $name => $value) {
            echo '<tr><td class="e">', credits_cell_impl((string) $name),
                ' </td><td class="v">', credits_cell_impl((string) $value),
                " </td></tr>\n";
        }
    }
    echo "</table>\n";
}

function phpinfo_variables_impl(): void
{
    echo "<h2>PHP Variables</h2>\n<table>\n";
    echo "<tr class=\"h\"><th>Variable</th><th>Value</th></tr>\n";
    foreach (['PHP_SELF', 'PHP_AUTH_TYPE', 'PHP_AUTH_USER', 'PHP_AUTH_PW'] as $name) {
        if (isset($GLOBALS[$name]) && \is_string($GLOBALS[$name])) {
            echo '<tr><td class="e">', $name, ' </td><td class="v">',
                credits_cell_impl($GLOBALS[$name]), " </td></tr>\n";
        }
    }
    foreach (['_REQUEST', '_GET', '_POST', '_FILES', '_COOKIE', '_SERVER', '_ENV'] as $name) {
        $values = $GLOBALS[$name] ?? [];
        if (!\is_array($values)) {
            continue;
        }
        foreach ($values as $key => $value) {
            $label = $name . "['" . (string) $key . "']";
            echo '<tr><td class="e">', credits_cell_impl($label),
                ' </td><td class="v">', phpinfo_cell_impl($value),
                " </td></tr>\n";
        }
    }
    $environment = \getenv();
    if (\is_array($environment)) {
        $server = $GLOBALS['_SERVER'] ?? [];
        foreach ($environment as $key => $value) {
            if (\is_array($server) && \array_key_exists($key, $server)) {
                continue;
            }
            $label = "_SERVER['" . (string) $key . "']";
            echo '<tr><td class="e">', credits_cell_impl($label),
                ' </td><td class="v">', credits_cell_impl((string) $value),
                " </td></tr>\n";
        }
    }
    echo "</table>\n";
}

function phpinfo_license_impl(): void
{
    echo "<h2>PHP License</h2>\n<table>\n<tr class=\"v\"><td>\n<p>\n";
    echo 'This program is free software; you can redistribute it and/or modify ';
    echo 'it under the terms of the PHP License as published by the PHP Group ';
    echo "and included in the distribution in the file:  LICENSE\n</p>\n<p>";
    echo 'This program is distributed in the hope that it will be useful, ';
    echo 'but WITHOUT ANY WARRANTY; without even the implied warranty of ';
    echo "MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.\n</p>\n<p>";
    echo 'If you did not receive a copy of the PHP license, or have any questions about ';
    echo "PHP licensing, please contact license@php.net.\n</p>\n";
    echo "</td></tr>\n</table>\n";
}

function phpinfo_impl(mixed $flags = INFO_ALL): true
{
    $flags = (int) $flags;
    $flags &= 4294967295;
    credits_html_head_impl();
    if (($flags & INFO_GENERAL) !== 0) {
        phpinfo_general_impl();
    }
    if (($flags & INFO_CONFIGURATION) !== 0) {
        echo "<hr />\n<h1>Configuration</h1>\n";
        if (($flags & INFO_MODULES) === 0) {
            echo "<h2>PHP Core</h2>\n";
            phpinfo_ini_table_impl(null);
        }
    }
    if (($flags & INFO_MODULES) !== 0) {
        phpinfo_modules_impl();
    }
    if (($flags & INFO_ENVIRONMENT) !== 0) {
        phpinfo_environment_impl();
    }
    if (($flags & INFO_VARIABLES) !== 0) {
        phpinfo_variables_impl();
    }
    if (($flags & INFO_CREDITS) !== 0) {
        echo "<hr />\n";
        phpcredits_impl(CREDITS_ALL & ~CREDITS_FULLPAGE);
    }
    if (($flags & INFO_LICENSE) !== 0) {
        phpinfo_license_impl();
    }
    echo '</div></body></html>';
    return true;
}

function phpversion_impl(?string $extension = null): string|false
{
    if ($extension === null) {
        return PHP_VERSION;
    }
    $versioned = [
        'core',
        'date',
        'hash',
        'json',
        'pcre',
        'random',
        'reflection',
        'standard',
        'spl',
    ];
    return \in_array(\strtolower($extension), $versioned, true)
        ? PHP_VERSION
        : false;
}

function inet_pton_impl(string $ip): string|false
{
    if (\strpos($ip, "\0") !== false) {
        throw new \ValueError(
            'inet_pton(): Argument #1 ($ip) must not contain any null bytes'
        );
    }

    // This linked engine has no IPv6 support. ip2long_impl supplies the same
    // strict four-part decimal grammar used by its AF_INET conversion.
    $number = ip2long_impl($ip);
    if ($number === false) {
        return false;
    }
    return \chr(($number >> 24) & 255) .
        \chr(($number >> 16) & 255) .
        \chr(($number >> 8) & 255) .
        \chr($number & 255);
}

function inet_ntop_impl(string $ip): string|false
{
    // Without IPv6 in the linked engine, only a four-byte AF_INET address is
    // accepted; all other binary lengths return false.
    if (\strlen($ip) !== 4) {
        return false;
    }
    return \ord($ip[0]) . '.' .
        \ord($ip[1]) . '.' .
        \ord($ip[2]) . '.' .
        \ord($ip[3]);
}

function ip2long_impl(string $ip): int|false
{
    $parts = \explode('.', $ip);
    if (\count($parts) !== 4) {
        return false;
    }

    $result = 0;
    foreach ($parts as $part) {
        $length = \strlen($part);
        if ($length === 0 || ($length > 1 && $part[0] === '0')) {
            return false;
        }

        $value = 0;
        for ($index = 0; $index < $length; $index++) {
            $byte = \ord($part[$index]);
            if ($byte < 48 || $byte > 57) {
                return false;
            }
            $value = $value * 10 + $byte - 48;
        }
        if ($value > 255) {
            return false;
        }
        $result = ($result << 8) | $value;
    }
    return $result;
}

function long2ip_impl(int $ip): string
{
    $ip &= 0xffffffff;
    return (($ip >> 24) & 255) . '.' .
        (($ip >> 16) & 255) . '.' .
        (($ip >> 8) & 255) . '.' .
        ($ip & 255);
}

function ini_alter_impl(string $option, string|int|float|bool|null $value): string|false
{
    return \ini_set($option, $value);
}

function ini_restore_impl(mixed $option): void
{
    \PHP2Go\Internal\ini_restore($option);
}

function assert_option_bool_impl(mixed $value): bool
{
    if (\is_array($value)) {
        \PHP2Go\Internal\warning('Array to string conversion');
        $text = 'Array';
    } else {
        $text = (string) $value;
    }
    $lower = \strtolower($text);
    return $text === '1' || $lower === 'on' ||
        $lower === 'yes' || $lower === 'true';
}

function assert_options_impl(
    mixed $option,
    mixed $value = null
): mixed {
    if ($option < 1 || $option > 5) {
        throw new \ValueError(
            'assert_options(): Argument #1 ($option) must be an ASSERT_* constant'
        );
    }
    if (\func_num_args() < 2) {
        return \PHP2Go\Internal\assert_options('get', $option);
    }
    if ($option === 2) {
        return \PHP2Go\Internal\assert_options('set', $option, $value);
    }
    return \PHP2Go\Internal\assert_options(
        'set',
        $option,
        assert_option_bool_impl($value)
    );
}

function natcasesort_impl(array &$array): true
{
    \uasort($array, static fn($left, $right) => \strnatcasecmp($left, $right));
    return true;
}

function natsort_impl(array &$array): true
{
    \uasort($array, static fn($left, $right) => \strnatcmp($left, $right));
    return true;
}

function str_shuffle_impl(string $string): string
{
    // The engine's php_string_shuffle is array_data_shuffle over the bytes:
    // walk from the last index down, swapping each with a uniformly chosen
    // earlier-or-equal one. Probed rather than assumed -- what is observable
    // about a randomised function is its invariants, and the engine keeps
    // three: the length, the multiset of bytes, and that a string of fewer
    // than two bytes comes back untouched.
    $length = \strlen($string);
    if ($length <= 1) {
        return $string;
    }

    $bytes = [];
    for ($index = 0; $index < $length; $index++) {
        $bytes[$index] = $string[$index];
    }
    for ($index = $length - 1; $index > 0; $index--) {
        $pick = \mt_rand(0, $index);
        if ($pick === $index) {
            continue;
        }
        $held = $bytes[$index];
        $bytes[$index] = $bytes[$pick];
        $bytes[$pick] = $held;
    }

    return \implode('', $bytes);
}

/**
 * print_r: the value as PHP renders it for a human, which is not var_dump's
 * format and not var_export's.
 *
 * The one thing this cannot answer in PHP is what an *object* shows -- a
 * Closure prints [name]/[file]/[line]/[parameter], a Generator prints
 * [function], and an enum case is labelled `S Enum:string` rather than
 * `S Object`, all built by the engine and reachable through no PHP function.
 * `\PHP2Go\Internal\print_r_object()` is that one question; everything below
 * is the formatting and the walk.
 */
function print_r_impl(mixed &$value, bool $return = false): string|bool
{
    // **By reference, for the same reason `print_r_render_impl` is.** php2go
    // copies an array eagerly, so a by-value parameter here hands the render a
    // clone whose identity differs from the array the program holds -- and an
    // element bound with `&$a` still points at the original. The guard then
    // sees two different identities for what is one array and detects the
    // recursion a level too late, printing one extra nesting before
    // `*RECURSION*`.
    $text = print_r_render_impl($value, 0, []);
    if ($return) {
        return $text;
    }
    echo $text;
    return true;
}

/**
 * One value, at one depth, knowing which containers the render is already
 * inside.
 *
 * **The subject is taken by reference, and that is what makes the recursion
 * check work.** php2go copies an array eagerly, so a by-value walk sees a fresh
 * identity at every level and `$a[] = &$a` would recurse for ever;
 * `\PHP2Go\Internal\array_id()` takes its argument by reference for the same
 * reason. Iterating by reference leaves no mark behind -- measured, with a
 * nested walk over an array holding a bound element, against `var_dump` before
 * and after.
 *
 * `$stack` is passed **by value** on purpose: it records the containers on the
 * path to here, not every container already seen, so the same array appearing
 * twice side by side prints twice rather than being called recursive.
 *
 * @param array<string, true> $stack
 */
function print_r_render_impl(mixed &$value, int $depth, array $stack): string
{
    $indent = \str_repeat('    ', $depth);
    $inner = \str_repeat('    ', $depth + 1);

    if (\is_array($value)) {
        $identity = 'a:' . \PHP2Go\Internal\array_id($value);
        if (isset($stack[$identity])) {
            // PHP writes the word and then the refusal on a line of its own,
            // indented by one space, with no closing parenthesis.
            return "Array\n *RECURSION*";
        }
        $stack[$identity] = true;
        $out = "Array\n" . $indent . "(\n";
        foreach ($value as $key => &$item) {
            $out .= $inner . '[' . $key . '] => '
                . print_r_render_impl($item, $depth + 2, $stack) . "\n";
        }
        unset($item);
        return $out . $indent . ")\n";
    }

    if (\is_object($value)) {
        $identity = 'o:' . \spl_object_id($value);
        if (isset($stack[$identity])) {
            // The label only: asking for the entries would run `__debugInfo` a
            // second time for an object this render is about to refuse.
            $named = \PHP2Go\Internal\print_r_object($value, false);
            return $named['label'] . "\n *RECURSION*";
        }
        $stack[$identity] = true;
        $shown = \PHP2Go\Internal\print_r_object($value, true);
        $out = $shown['label'] . "\n" . $indent . "(\n";
        foreach ($shown['entries'] as $label => &$item) {
            $out .= $inner . '[' . $label . '] => '
                . print_r_render_impl($item, $depth + 2, $stack) . "\n";
        }
        unset($item);
        return $out . $indent . ")\n";
    }

    return (string) $value;
}

function ucfirst_impl(string $string): string
{
    // ASCII only, which is the engine's rule and not an approximation:
    // ucfirst("\xe9tre") comes back unchanged, because a PHP string is bytes
    // and this function knows nothing about encodings.
    if ($string === '') {
        return $string;
    }
    $byte = \ord($string[0]);
    if ($byte >= 97 && $byte <= 122) {
        $string[0] = \chr($byte - 32);
    }
    return $string;
}

function lcfirst_impl(string $string): string
{
    if ($string === '') {
        return $string;
    }
    $byte = \ord($string[0]);
    if ($byte >= 65 && $byte <= 90) {
        $string[0] = \chr($byte + 32);
    }
    return $string;
}

function ucwords_impl(string $string, string $separators = " \t\r\n\f\v"): string
{
    // The separator list **replaces** the default rather than adding to it,
    // so ucwords("hello world-foo bar") leaves "foo" alone while
    // ucwords("a-b_c", "-_") capitalises after both. The first byte always
    // counts as following a separator.
    $length = \strlen($string);
    $atWordStart = true;
    for ($index = 0; $index < $length; $index++) {
        $character = $string[$index];
        if ($atWordStart) {
            $byte = \ord($character);
            if ($byte >= 97 && $byte <= 122) {
                $string[$index] = \chr($byte - 32);
            }
        }
        $atWordStart = $separators !== '' && \strpos($separators, $character) !== false;
    }
    return $string;
}

function nl2br_impl(string $string, bool $use_xhtml = true): string
{
    // A CRLF or LFCR **pair is one break**, and the original bytes are kept
    // after the tag rather than replaced -- nl2br("a\r\nb") is
    // "a<br />\r\nb", not "a<br />b".
    $break = $use_xhtml ? '<br />' : '<br>';
    $length = \strlen($string);
    $result = '';
    for ($index = 0; $index < $length; $index++) {
        $character = $string[$index];
        if ($character !== "\n" && $character !== "\r") {
            $result .= $character;
            continue;
        }
        $result .= $break . $character;
        if ($index + 1 < $length) {
            $next = $string[$index + 1];
            if (($next === "\n" || $next === "\r") && $next !== $character) {
                $result .= $next;
                $index++;
            }
        }
    }
    return $result;
}

function quotemeta_impl(string $string): string
{
    // The set the engine escapes, read back from it: braces are **not** in it.
    $result = '';
    $length = \strlen($string);
    for ($index = 0; $index < $length; $index++) {
        $character = $string[$index];
        if (\strpos('.\\+*?[^]$()', $character) !== false) {
            $result .= '\\';
        }
        $result .= $character;
    }
    return $result;
}

function strrev_impl(string $string): string
{
    $result = '';
    for ($index = \strlen($string) - 1; $index >= 0; $index--) {
        $result .= $string[$index];
    }
    return $result;
}

function str_pad_impl(
    string $string,
    int $length,
    string $pad_string = ' ',
    int $pad_type = \STR_PAD_RIGHT
): string {
    if ($pad_string === '') {
        throw new \ValueError('str_pad(): Argument #3 ($pad_string) must not be empty');
    }
    if ($pad_type !== \STR_PAD_LEFT && $pad_type !== \STR_PAD_RIGHT && $pad_type !== \STR_PAD_BOTH) {
        throw new \ValueError(
            'str_pad(): Argument #4 ($pad_type) must be STR_PAD_LEFT, STR_PAD_RIGHT, or STR_PAD_BOTH'
        );
    }

    $have = \strlen($string);
    if ($length <= $have) {
        return $string;
    }
    $total = $length - $have;
    $left = 0;
    $right = $total;
    if ($pad_type === \STR_PAD_LEFT) {
        $left = $total;
        $right = 0;
    } elseif ($pad_type === \STR_PAD_BOTH) {
        // An odd amount puts the extra byte on the right.
        $left = \intdiv($total, 2);
        $right = $total - $left;
    }

    return str_pad_run_impl($pad_string, $left) . $string . str_pad_run_impl($pad_string, $right);
}

function str_pad_run_impl(string $pad_string, int $width): string
{
    if ($width <= 0) {
        return '';
    }
    $run = '';
    $size = \strlen($pad_string);
    for ($index = 0; $index < $width; $index++) {
        $run .= $pad_string[$index % $size];
    }
    return $run;
}

function array_sum_impl(array $array): int|float
{
    // `+=` is the language's own addition, so the coercions and the
    // diagnostics an unsupported element earns come from the operator rather
    // than being restated here. That is the whole argument for writing this in
    // PHP.
    $sum = 0;
    foreach ($array as $value) {
        $sum += $value;
    }
    return $sum;
}

function array_product_impl(array $array): int|float
{
    $product = 1;
    foreach ($array as $value) {
        $product *= $value;
    }
    return $product;
}


function array_flip_impl(array $array): array
{
    $flipped = [];
    foreach ($array as $key => $value) {
        if (!\is_int($value) && !\is_string($value)) {
            // \PHP2Go\Internal\warning re-attributes the diagnostic to the
            // caller of the library function, which is where the engine raises
            // it from. `trigger_error` would name this file and this line.
            \PHP2Go\Internal\warning(
                'array_flip(): Can only flip string and integer values, entry skipped'
            );
            continue;
        }
        $flipped[$value] = $key;
    }
    return $flipped;
}

function str_rot13_impl(string $string): string
{
    return \strtr(
        $string,
        'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz',
        'NOPQRSTUVWXYZABCDEFGHIJKLMnopqrstuvwxyzabcdefghijklm'
    );
}

function fpow_impl(float $num, float $exponent): float
{
    return \pow($num, $exponent);
}

function quoted_printable_encode_impl(string $string): string
{
    $result = '';
    $line_length = 0;
    $length = \strlen($string);
    for ($index = 0; $index < $length; $index++) {
        $character = $string[$index];
        $byte = \ord($character);

        if ($character === "\r" && $index + 1 < $length && $string[$index + 1] === "\n") {
            $result .= "\r\n";
            $line_length = 0;
            $index++;
            continue;
        }

        $encode = $byte < 32 || $byte === 127 || $byte >= 128 || $character === '=' ||
            ($character === ' ' && $index + 1 < $length && $string[$index + 1] === "\r");
        if ($encode) {
            $line_length += 3;
            $break = ($byte <= 127 && $line_length > 75) ||
                ($byte >= 128 && $byte <= 223 && $line_length + 3 > 75) ||
                ($byte >= 224 && $byte <= 239 && $line_length + 6 > 75) ||
                ($byte >= 240 && $byte <= 244 && $line_length + 9 > 75);
            if ($break) {
                $result .= "=\r\n";
                $line_length = 3;
            }
            $result .= \sprintf('=%02X', $byte);
        } else {
            $line_length++;
            if ($line_length > 75) {
                $result .= "=\r\n";
                $line_length = 1;
            }
            $result .= $character;
        }
    }
    return $result;
}

function quoted_printable_decode_impl(string $string): string
{
    $result = '';
    $index = 0;
    $length = \strlen($string);
    while ($index < $length) {
        if ($string[$index] === "\0") {
            break;
        }
        if ($string[$index] !== '=') {
            $result .= $string[$index];
            $index++;
            continue;
        }

        if ($index + 1 === $length) {
            break;
        }

        if ($index + 2 < $length) {
            $high = url_hex_value_impl($string[$index + 1]);
            $low = url_hex_value_impl($string[$index + 2]);
            if ($high >= 0 && $low >= 0) {
                $result .= \chr($high * 16 + $low);
                $index += 3;
                continue;
            }
        }

        $line_end = $index + 1;
        while ($line_end < $length &&
            ($string[$line_end] === ' ' || $string[$line_end] === "\t")) {
            $line_end++;
        }
        if ($line_end === $length) {
            break;
        }
        if ($string[$line_end] === "\n") {
            $index = $line_end + 1;
            continue;
        }
        if ($string[$line_end] === "\r") {
            $index = $line_end + 1;
            if ($index < $length && $string[$index] === "\n") {
                $index++;
            }
            continue;
        }

        $result .= '=';
        $index++;
    }
    return $result;
}

function soundex_impl(string $string): string
{
    $result = '';
    $previous_code = -1;
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $byte = \ord($string[$index]);
        if ($byte >= 97 && $byte <= 122) {
            $byte -= 32;
        } elseif ($byte < 65 || $byte > 90) {
            continue;
        }

        $letter = \chr($byte);
        $code = soundex_code_impl($letter);
        if ($result === '') {
            $result = $letter;
            $previous_code = $code;
            continue;
        }
        if ($code === 0) {
            $previous_code = 0;
            continue;
        }
        if ($code !== $previous_code) {
            $result .= (string) $code;
            if (\strlen($result) === 4) {
                return $result;
            }
        }
        $previous_code = $code;
    }

    return \str_pad($result, 4, '0');
}

function soundex_code_impl(string $letter): int
{
    if (\strpos('BFPV', $letter) !== false) {
        return 1;
    }
    if (\strpos('CGJKQSXZ', $letter) !== false) {
        return 2;
    }
    if (\strpos('DT', $letter) !== false) {
        return 3;
    }
    if ($letter === 'L') {
        return 4;
    }
    if (\strpos('MN', $letter) !== false) {
        return 5;
    }
    if ($letter === 'R') {
        return 6;
    }
    return 0;
}

function str_increment_impl(string $string): string
{
    string_increment_validate_impl($string, 'str_increment');
    $result = $string;
    for ($index = \strlen($result) - 1; $index >= 0; $index--) {
        $byte = \ord($result[$index]);
        if (($byte >= 48 && $byte <= 56) || ($byte >= 65 && $byte <= 89) ||
            ($byte >= 97 && $byte <= 121)) {
            $result[$index] = \chr($byte + 1);
            return $result;
        }
        if ($byte === 57) {
            $result[$index] = '0';
        } elseif ($byte === 90) {
            $result[$index] = 'A';
        } else {
            $result[$index] = 'a';
        }
    }

    $first = $string[0];
    if ($first >= '0' && $first <= '9') {
        return '1' . $result;
    }
    return ($first >= 'A' && $first <= 'Z' ? 'A' : 'a') . $result;
}

function str_decrement_impl(string $string): string
{
    string_increment_validate_impl($string, 'str_decrement');
    if ($string[0] === '0') {
        throw new \ValueError(
            'str_decrement(): Argument #1 ($string) "' . $string . '" is out of decrement range'
        );
    }

    $result = $string;
    for ($index = \strlen($result) - 1; $index >= 0; $index--) {
        $byte = \ord($result[$index]);
        if (($byte >= 49 && $byte <= 57) || ($byte >= 66 && $byte <= 90) ||
            ($byte >= 98 && $byte <= 122)) {
            $result[$index] = \chr($byte - 1);
            if ($index === 0 && $byte === 49 && \strlen($result) > 1) {
                return \substr($result, 1);
            }
            return $result;
        }
        if ($byte === 48) {
            $result[$index] = '9';
        } elseif ($byte === 65) {
            $result[$index] = 'Z';
        } else {
            $result[$index] = 'z';
        }
    }

    if (\strlen($result) === 1) {
        throw new \ValueError(
            'str_decrement(): Argument #1 ($string) "' . $string . '" is out of decrement range'
        );
    }
    return \substr($result, 1);
}

function string_increment_validate_impl(string $string, string $function): void
{
    if ($string === '') {
        throw new \ValueError($function . '(): Argument #1 ($string) must not be empty');
    }
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $byte = \ord($string[$index]);
        if (!(($byte >= 48 && $byte <= 57) || ($byte >= 65 && $byte <= 90) ||
            ($byte >= 97 && $byte <= 122))) {
            throw new \ValueError(
                $function . '(): Argument #1 ($string) must be composed only of alphanumeric ASCII characters'
            );
        }
    }
}

function rawurldecode_impl(string $string): string
{
    return url_decode_impl($string, false);
}

function rawurlencode_impl(string $string): string
{
    $result = '';
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $character = $string[$index];
        $byte = \ord($character);
        if (url_is_alnum_impl($byte) || $character === '-' || $character === '_' ||
            $character === '.' || $character === '~') {
            $result .= $character;
        } else {
            $result .= \sprintf('%%%02X', $byte);
        }
    }

    return $result;
}

function urldecode_impl(string $string): string
{
    return url_decode_impl($string, true);
}

function urlencode_impl(string $string): string
{
    $result = '';
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $character = $string[$index];
        $byte = \ord($character);
        if (url_is_alnum_impl($byte) || $character === '-' || $character === '_' ||
            $character === '.') {
            $result .= $character;
        } elseif ($character === ' ') {
            $result .= '+';
        } else {
            $result .= \sprintf('%%%02X', $byte);
        }
    }

    return $result;
}

function url_decode_impl(string $string, bool $plus_as_space): string
{
    $result = '';
    $index = 0;
    $length = \strlen($string);
    while ($index < $length) {
        $character = $string[$index];
        if ($plus_as_space && $character === '+') {
            $result .= ' ';
            $index++;
            continue;
        }

        if ($character === '%' && $index + 2 < $length) {
            $high = url_hex_value_impl($string[$index + 1]);
            $low = url_hex_value_impl($string[$index + 2]);
            if ($high >= 0 && $low >= 0) {
                $result .= \chr($high * 16 + $low);
                $index += 3;
                continue;
            }
        }

        $result .= $character;
        $index++;
    }

    return $result;
}

function url_hex_value_impl(string $character): int
{
    $byte = \ord($character);
    if ($byte >= 48 && $byte <= 57) {
        return $byte - 48;
    }
    if ($byte >= 65 && $byte <= 70) {
        return $byte - 55;
    }
    if ($byte >= 97 && $byte <= 102) {
        return $byte - 87;
    }
    return -1;
}

function url_is_alnum_impl(int $byte): bool
{
    return ($byte >= 48 && $byte <= 57) ||
        ($byte >= 65 && $byte <= 90) ||
        ($byte >= 97 && $byte <= 122);
}

function stripcslashes_impl(string $string): string
{
    $result = '';
    $length = \strlen($string);
    $escapes = [
        'a' => "\x07",
        'b' => "\x08",
        'f' => "\x0c",
        'n' => "\n",
        'r' => "\r",
        't' => "\t",
        'v' => "\x0b",
    ];
    for ($index = 0; $index < $length; $index++) {
        if ($string[$index] !== '\\' || $index + 1 === $length) {
            $result .= $string[$index];
            continue;
        }

        $next = $string[++$index];
        if (\array_key_exists($next, $escapes)) {
            $result .= $escapes[$next];
            continue;
        }
        if ($next === 'x') {
            $value = 0;
            $digits = 0;
            while ($digits < 2 && $index + 1 < $length) {
                $digit = url_hex_value_impl($string[$index + 1]);
                if ($digit < 0) {
                    break;
                }
                $value = $value * 16 + $digit;
                $digits++;
                $index++;
            }
            $result .= $digits === 0 ? 'x' : \chr($value);
            continue;
        }

        $byte = \ord($next);
        if ($byte >= 48 && $byte <= 55) {
            $value = $byte - 48;
            $digits = 1;
            while ($digits < 3 && $index + 1 < $length) {
                $byte = \ord($string[$index + 1]);
                if ($byte < 48 || $byte > 55) {
                    break;
                }
                $value = $value * 8 + $byte - 48;
                $digits++;
                $index++;
            }
            $result .= \chr($value);
            continue;
        }
        $result .= $next;
    }
    return $result;
}

function strcspn_impl(
    string $string,
    string $characters,
    int $offset = 0,
    ?int $length = null
): int {
    return string_span_impl($string, $characters, $offset, $length, false);
}

function strspn_impl(
    string $string,
    string $characters,
    int $offset = 0,
    ?int $length = null
): int {
    return string_span_impl($string, $characters, $offset, $length, true);
}

function string_span_impl(
    string $string,
    string $characters,
    int $offset,
    ?int $length,
    bool $accept_contained
): int {
    $slice = \substr($string, $offset, $length);
    $count = 0;
    for ($index = 0, $size = \strlen($slice); $index < $size; $index++) {
        $contained = \strpos($characters, $slice[$index]) !== false;
        if ($contained !== $accept_contained) {
            break;
        }
        $count++;
    }
    return $count;
}

function addcslashes_impl(string $string, string $characters): string
{
    if ($string === '' || $characters === '') {
        return $string;
    }

    $mask = character_mask_impl($characters, 'addcslashes');
    $result = '';
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $character = $string[$index];
        $byte = \ord($character);
        if (!$mask[$byte]) {
            $result .= $character;
            continue;
        }

        if ($byte >= 32 && $byte <= 126) {
            $result .= '\\' . $character;
            continue;
        }

        $escaped = match ($byte) {
            7 => 'a',
            8 => 'b',
            9 => 't',
            10 => 'n',
            11 => 'v',
            12 => 'f',
            13 => 'r',
            default => \sprintf('%03o', $byte),
        };
        $result .= '\\' . $escaped;
    }
    return $result;
}

function character_mask_impl(string $characters, string $function): array
{
    $mask = \array_fill(0, 256, false);
    $length = \strlen($characters);
    for ($index = 0; $index < $length; $index++) {
        $byte = \ord($characters[$index]);
        if ($index + 3 < $length && $characters[$index + 1] === '.' &&
            $characters[$index + 2] === '.' &&
            \ord($characters[$index + 3]) >= $byte) {
            $end = \ord($characters[$index + 3]);
            for ($value = $byte; $value <= $end; $value++) {
                $mask[$value] = true;
            }
            $index += 3;
            continue;
        }

        if ($index + 1 < $length && $characters[$index] === '.' &&
            $characters[$index + 1] === '.') {
            if ($index === 0) {
                $message = "Invalid '..'-range, no character to the left of '..'";
            } elseif ($index + 2 >= $length) {
                $message = "Invalid '..'-range, no character to the right of '..'";
            } elseif (\ord($characters[$index - 1]) > \ord($characters[$index + 2])) {
                $message = "Invalid '..'-range, '..'-range needs to be incrementing";
            } else {
                $message = "Invalid '..'-range";
            }
            \PHP2Go\Internal\warning($function . '(): ' . $message);
            continue;
        }
        $mask[$byte] = true;
    }
    return $mask;
}

function convert_uuencode_impl(string $string): string
{
    $result = '';
    $length = \strlen($string);
    for ($offset = 0; $offset < $length; $offset += 45) {
        $chunk_length = \min(45, $length - $offset);
        $chunk = \substr($string, $offset, $chunk_length);
        $result .= uuencode_character_impl($chunk_length);

        for ($index = 0; $index < $chunk_length; $index += 3) {
            $first = \ord($chunk[$index]);
            $second = $index + 1 < $chunk_length ? \ord($chunk[$index + 1]) : 0;
            $third = $index + 2 < $chunk_length ? \ord($chunk[$index + 2]) : 0;

            $result .= uuencode_character_impl($first >> 2);
            $result .= uuencode_character_impl((($first << 4) & 48) | (($second >> 4) & 15));
            $result .= uuencode_character_impl((($second << 2) & 60) | (($third >> 6) & 3));
            $result .= uuencode_character_impl($third & 63);
        }
        $result .= "\n";
    }

    return $result . \chr(96) . "\n";
}

function uuencode_character_impl(int $value): string
{
    return $value === 0 ? \chr(96) : \chr(($value & 63) + 32);
}

function convert_uudecode_impl(string $string): string|false
{
    $source_length = \strlen($string);
    if ($source_length === 0) {
        return uudecode_invalid_impl();
    }

    $offset = 0;
    $total_length = 0;
    $result = '';
    while ($offset < $source_length) {
        $line_length = uudecode_value_impl($string[$offset]);
        $offset++;
        if ($line_length === 0) {
            break;
        }
        if ($line_length > $source_length) {
            return uudecode_invalid_impl();
        }
        $total_length += $line_length;

        $encoded_length = $line_length === 45 ? 60 : (int) \floor($line_length * 1.33);
        $line_end = $offset + $encoded_length;
        if ($line_end > $source_length) {
            return uudecode_invalid_impl();
        }

        while ($offset < $line_end) {
            if ($offset + 4 > $source_length) {
                return uudecode_invalid_impl();
            }
            $first = uudecode_value_impl($string[$offset]);
            $second = uudecode_value_impl($string[$offset + 1]);
            $third = uudecode_value_impl($string[$offset + 2]);
            $fourth = uudecode_value_impl($string[$offset + 3]);
            $result .= \chr(($first << 2) | ($second >> 4));
            $result .= \chr(($second << 4) | ($third >> 2));
            $result .= \chr(($third << 6) | $fourth);
            $offset += 4;
        }

        if ($line_length < 45) {
            break;
        }
        $offset++;
    }

    return \substr($result, 0, $total_length);
}

function uudecode_value_impl(string $character): int
{
    return (\ord($character) - 32) & 63;
}

function uudecode_invalid_impl(): false
{
    \PHP2Go\Internal\warning(
        'convert_uudecode(): Argument #1 ($data) is not a valid uuencoded string'
    );
    return false;
}

function version_compare_impl(
    string $version1,
    string $version2,
    ?string $operator = null
): int|bool {
    $compare = version_compare_value_impl($version1, $version2);
    if ($operator === null) {
        return $compare;
    }

    return match ($operator) {
        '<', 'lt' => $compare === -1,
        '<=', 'le' => $compare !== 1,
        '>', 'gt' => $compare === 1,
        '>=', 'ge' => $compare !== -1,
        '==', '=', 'eq' => $compare === 0,
        '!=', '<>', 'ne' => $compare !== 0,
        default => throw new \ValueError(
            'version_compare(): Argument #3 ($operator) must be a valid comparison operator'
        ),
    };
}

function version_compare_value_impl(string $version1, string $version2): int
{
    $version1 = version_truncate_nul_impl($version1);
    $version2 = version_truncate_nul_impl($version2);
    if ($version1 === '' || $version2 === '') {
        if ($version1 === $version2) {
            return 0;
        }
        return $version1 === '' ? -1 : 1;
    }

    if ($version1[0] !== '#') {
        $version1 = version_canonicalize_impl($version1);
    }
    if ($version2[0] !== '#') {
        $version2 = version_canonicalize_impl($version2);
    }

    // The native scanner initializes its next-component pointers before it
    // sees that a canonicalized value is empty. It therefore takes the left
    // remainder branch rather than comparing two empty component lists.
    if ($version1 === '' || $version2 === '') {
        if ($version1 !== '' && version_is_digit_impl($version1[0])) {
            return 1;
        }
        return version_compare_value_impl($version1, '#N#');
    }

    $parts1 = \explode('.', $version1);
    $parts2 = \explode('.', $version2);
    $common = \min(\count($parts1), \count($parts2));
    for ($index = 0; $index < $common; $index++) {
        $compare = version_component_compare_impl($parts1[$index], $parts2[$index]);
        if ($compare !== 0) {
            return $compare;
        }
    }

    if (\count($parts1) > $common) {
        $remaining = \implode('.', \array_slice($parts1, $common));
        if ($remaining !== '' && version_is_digit_impl($remaining[0])) {
            return 1;
        }
        return version_compare_value_impl($remaining, '#N#');
    }
    if (\count($parts2) > $common) {
        $remaining = \implode('.', \array_slice($parts2, $common));
        if ($remaining !== '' && version_is_digit_impl($remaining[0])) {
            return -1;
        }
        return version_compare_value_impl('#N#', $remaining);
    }
    return 0;
}

function version_truncate_nul_impl(string $version): string
{
    $nul = \strpos($version, "\0");
    return $nul === false ? $version : \substr($version, 0, $nul);
}

function version_canonicalize_impl(string $version): string
{
    $result = $version[0];
    $previous = $version[0];
    for ($index = 1, $length = \strlen($version); $index < $length; $index++) {
        $character = $version[$index];
        $last = $result[\strlen($result) - 1];

        if ($character === '-' || $character === '_' || $character === '+') {
            if ($last !== '.') {
                $result .= '.';
            }
        } else {
            $previous_digit = version_is_digit_impl($previous);
            $current_digit = version_is_digit_impl($character);
            $previous_nondigit = !$previous_digit && $previous !== '.';
            $current_nondigit = !$current_digit && $character !== '.';
            if (($previous_nondigit && $current_digit) ||
                ($previous_digit && $current_nondigit)) {
                if ($last !== '.') {
                    $result .= '.';
                }
                $result .= $character;
            } elseif (!version_is_alnum_impl($character)) {
                if ($last !== '.') {
                    $result .= '.';
                }
            } else {
                $result .= $character;
            }
        }
        $previous = $character;
    }

    if ($result !== '' && $result[\strlen($result) - 1] === '.') {
        return \substr($result, 0, -1);
    }
    return $result;
}

function version_component_compare_impl(string $left, string $right): int
{
    $left_digit = $left !== '' && version_is_digit_impl($left[0]);
    $right_digit = $right !== '' && version_is_digit_impl($right[0]);
    if ($left_digit && $right_digit) {
        $left_value = (int) $left;
        $right_value = (int) $right;
        if ($left_value === $right_value) {
            return 0;
        }
        return $left_value < $right_value ? -1 : 1;
    }
    if (!$left_digit && !$right_digit) {
        $left_rank = version_special_rank_impl($left);
        $right_rank = version_special_rank_impl($right);
    } elseif ($left_digit) {
        $left_rank = version_special_rank_impl('#N#');
        $right_rank = version_special_rank_impl($right);
    } else {
        $left_rank = version_special_rank_impl($left);
        $right_rank = version_special_rank_impl('#N#');
    }

    if ($left_rank === $right_rank) {
        return 0;
    }
    return $left_rank < $right_rank ? -1 : 1;
}

function version_special_rank_impl(string $component): int
{
    $forms = [
        ['dev', 0],
        ['alpha', 1],
        ['a', 1],
        ['beta', 2],
        ['b', 2],
        ['RC', 3],
        ['rc', 3],
        ['#', 4],
        ['pl', 5],
        ['p', 5],
    ];
    foreach ($forms as $form) {
        if (\substr($component, 0, \strlen($form[0])) === $form[0]) {
            return $form[1];
        }
    }
    return -1;
}

function version_is_digit_impl(string $character): bool
{
    if ($character === '') {
        return false;
    }
    $byte = \ord($character);
    return $byte >= 48 && $byte <= 57;
}

function version_is_alnum_impl(string $character): bool
{
    if ($character === '') {
        return false;
    }
    $byte = \ord($character);
    return ($byte >= 48 && $byte <= 57) ||
        ($byte >= 65 && $byte <= 90) ||
        ($byte >= 97 && $byte <= 122);
}

function metaphone_impl(string $string, int $max_phonemes = 0): string
{
    if ($max_phonemes < 0) {
        throw new \ValueError(
            'metaphone(): Argument #2 ($max_phonemes) must be greater than or equal to 0'
        );
    }

    $nul = \strpos($string, "\0");
    if ($nul !== false) {
        $string = \substr($string, 0, $nul);
    }
    $length = \strlen($string);
    $index = 0;
    while ($index < $length && !metaphone_is_alpha_impl($string[$index])) {
        $index++;
    }
    if ($index === $length) {
        return '';
    }

    $result = '';
    $current = metaphone_upper_impl($string[$index]);
    if ($current === 'A') {
        if (metaphone_at_impl($string, $index + 1, $length) === 'E') {
            $result .= 'E';
            $index += 2;
        } else {
            $result .= 'A';
            $index++;
        }
    } elseif ($current === 'G' || $current === 'K' || $current === 'P') {
        if (metaphone_at_impl($string, $index + 1, $length) === 'N') {
            $result .= 'N';
            $index += 2;
        }
    } elseif ($current === 'W') {
        $next = metaphone_at_impl($string, $index + 1, $length);
        if ($next === 'R') {
            $result .= 'R';
            $index += 2;
        } elseif ($next === 'H' || metaphone_is_vowel_impl($next)) {
            $result .= 'W';
            $index += 2;
        }
    } elseif ($current === 'X') {
        $result .= 'S';
        $index++;
    } elseif (metaphone_is_vowel_impl($current)) {
        $result .= $current;
        $index++;
    }

    while ($index < $length &&
        ($max_phonemes === 0 || \strlen($result) < $max_phonemes)) {
        $raw = $string[$index];
        if (!metaphone_is_alpha_impl($raw)) {
            $index++;
            continue;
        }

        $current = metaphone_upper_impl($raw);
        $previous = $index > 0 ? metaphone_upper_impl($string[$index - 1]) : "\0";
        if ($current === $previous && $current !== 'C') {
            $index++;
            continue;
        }

        $next = metaphone_at_impl($string, $index + 1, $length);
        $after_next = metaphone_at_impl($string, $index + 2, $length);
        $skip = 0;

        if ($current === 'B') {
            if ($previous !== 'M') {
                $result .= 'B';
            }
        } elseif ($current === 'C') {
            if (metaphone_makes_soft_impl($next)) {
                if ($next === 'I' && $after_next === 'A') {
                    $result .= 'X';
                } elseif ($previous !== 'S') {
                    $result .= 'S';
                }
            } elseif ($next === 'H') {
                $result .= 'X';
                $skip = 1;
            } else {
                $result .= 'K';
            }
        } elseif ($current === 'D') {
            if ($next === 'G' && metaphone_makes_soft_impl($after_next)) {
                $result .= 'J';
                $skip = 1;
            } else {
                $result .= 'T';
            }
        } elseif ($current === 'G') {
            if ($next === 'H') {
                $back_three = $index >= 3
                    ? metaphone_upper_impl($string[$index - 3])
                    : "\0";
                $back_four = $index >= 4
                    ? metaphone_upper_impl($string[$index - 4])
                    : "\0";
                if (!metaphone_no_gh_to_f_impl($back_three) && $back_four !== 'H') {
                    $result .= 'F';
                    $skip = 1;
                }
            } elseif ($next === 'N') {
                $ahead_three = metaphone_at_impl($string, $index + 3, $length);
                if (metaphone_is_alpha_impl($after_next) &&
                    !($after_next === 'E' && $ahead_three === 'D')) {
                    $result .= 'K';
                }
            } elseif (metaphone_makes_soft_impl($next) && $previous !== 'G') {
                $result .= 'J';
            } else {
                $result .= 'K';
            }
        } elseif ($current === 'H') {
            if (metaphone_is_vowel_impl($next) &&
                !metaphone_affects_h_impl($previous)) {
                $result .= 'H';
            }
        } elseif ($current === 'K') {
            if ($previous !== 'C') {
                $result .= 'K';
            }
        } elseif ($current === 'P') {
            $result .= $next === 'H' ? 'F' : 'P';
        } elseif ($current === 'Q') {
            $result .= 'K';
        } elseif ($current === 'S') {
            if (($next === 'I' && ($after_next === 'O' || $after_next === 'A')) ||
                $next === 'H') {
                $result .= 'X';
                if ($next === 'H') {
                    $skip = 1;
                }
            } else {
                $result .= 'S';
            }
        } elseif ($current === 'T') {
            if ($next === 'I' && ($after_next === 'O' || $after_next === 'A')) {
                $result .= 'X';
            } elseif ($next === 'H') {
                $result .= '0';
                $skip = 1;
            } elseif (!($next === 'C' && $after_next === 'H')) {
                $result .= 'T';
            }
        } elseif ($current === 'V') {
            $result .= 'F';
        } elseif ($current === 'W') {
            if (metaphone_is_vowel_impl($next)) {
                $result .= 'W';
            }
        } elseif ($current === 'X') {
            $result .= 'KS';
        } elseif ($current === 'Y') {
            if (metaphone_is_vowel_impl($next)) {
                $result .= 'Y';
            }
        } elseif ($current === 'Z') {
            $result .= 'S';
        } elseif (\strpos('FJLMNR', $current) !== false) {
            $result .= $current;
        }

        $index += $skip + 1;
    }
    return $result;
}

function metaphone_upper_impl(string $character): string
{
    if ($character === '') {
        return "\0";
    }
    $byte = \ord($character);
    if ($byte >= 97 && $byte <= 122) {
        return \chr($byte - 32);
    }
    return $character;
}

function metaphone_at_impl(string $string, int $index, int $length): string
{
    if ($index < 0 || $index >= $length) {
        return "\0";
    }
    return metaphone_upper_impl($string[$index]);
}

function metaphone_is_alpha_impl(string $character): bool
{
    if ($character === '') {
        return false;
    }
    $byte = \ord($character);
    return ($byte >= 65 && $byte <= 90) || ($byte >= 97 && $byte <= 122);
}

function metaphone_is_vowel_impl(string $character): bool
{
    return \strpos('AEIOU', $character) !== false;
}

function metaphone_makes_soft_impl(string $character): bool
{
    return \strpos('EIY', $character) !== false;
}

function metaphone_affects_h_impl(string $character): bool
{
    return \strpos('CGPST', $character) !== false;
}

function metaphone_no_gh_to_f_impl(string $character): bool
{
    return \strpos('BDH', $character) !== false;
}

function utf8_encode_impl(string $string): string
{
    \PHP2Go\Internal\deprecated(
        'Function utf8_encode() is deprecated since 8.2, ' .
        'visit the php.net documentation for various alternatives'
    );

    $result = '';
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $byte = \ord($string[$index]);
        if ($byte < 128) {
            $result .= $string[$index];
        } else {
            $result .= \chr(192 | ($byte >> 6));
            $result .= \chr(128 | ($byte & 63));
        }
    }
    return $result;
}

function utf8_decode_impl(string $string): string
{
    \PHP2Go\Internal\deprecated(
        'Function utf8_decode() is deprecated since 8.2, ' .
        'visit the php.net documentation for various alternatives'
    );

    $result = '';
    $index = 0;
    $length = \strlen($string);
    while ($index < $length) {
        $byte = \ord($string[$index]);
        if ($byte < 128) {
            $result .= $string[$index];
            $index++;
            continue;
        }
        if ($byte < 194) {
            $result .= '?';
            $index++;
            continue;
        }
        if ($byte < 224) {
            if ($index + 1 >= $length) {
                $result .= '?';
                $index++;
                continue;
            }
            $next = \ord($string[$index + 1]);
            if (!utf8_is_trail_impl($next)) {
                $result .= '?';
                $index += utf8_is_lead_impl($next) ? 1 : 2;
                continue;
            }
            $codepoint = (($byte & 31) << 6) | ($next & 63);
            $result .= $codepoint <= 255 ? \chr($codepoint) : '?';
            $index += 2;
            continue;
        }
        if ($byte < 240) {
            $advance = utf8_invalid_advance_impl($string, $index, $length, 3);
            $result .= '?';
            $index += $advance;
            continue;
        }
        if ($byte < 245) {
            $advance = utf8_invalid_advance_impl($string, $index, $length, 4);
            $result .= '?';
            $index += $advance;
            continue;
        }
        $result .= '?';
        $index++;
    }
    return $result;
}

function utf8_invalid_advance_impl(
    string $string,
    int $index,
    int $length,
    int $width
): int {
    for ($offset = 1; $offset < $width; $offset++) {
        if ($index + $offset >= $length) {
            return $offset;
        }
        $byte = \ord($string[$index + $offset]);
        if (!utf8_is_trail_impl($byte)) {
            if (utf8_is_lead_impl($byte)) {
                return $offset;
            }
            continue;
        }
    }
    return $width;
}

function utf8_is_lead_impl(int $byte): bool
{
    return $byte < 128 || ($byte >= 194 && $byte <= 244);
}

function utf8_is_trail_impl(int $byte): bool
{
    return $byte >= 128 && $byte <= 191;
}

function hebrev_impl(string $string, int $max_chars_per_line = 0): string
{
    $length = \strlen($string);
    if ($length === 0) {
        return '';
    }

    $block_start = 0;
    $block_end = 0;
    $cursor = 0;
    $hebrew_block = hebrev_is_hebrew_impl($string[0]);
    $visual = '';

    do {
        if ($hebrew_block) {
            while ($block_end < $length - 1) {
                $next = $string[$cursor + 1];
                if (!hebrev_is_hebrew_impl($next) &&
                    !hebrev_is_blank_impl($next) &&
                    !hebrev_is_punctuation_impl($next) &&
                    $next !== "\n") {
                    break;
                }
                $cursor++;
                $block_end++;
            }

            $segment = '';
            for ($index = $block_end; $index >= $block_start; $index--) {
                $segment .= hebrev_mirror_impl($string[$index]);
            }
            $visual = $segment . $visual;
            $hebrew_block = false;
        } else {
            while ($block_end < $length - 1 &&
                !hebrev_is_hebrew_impl($string[$cursor + 1]) &&
                $string[$cursor + 1] !== "\n") {
                $cursor++;
                $block_end++;
            }
            while ($block_end > $block_start &&
                (hebrev_is_blank_impl($string[$cursor]) ||
                 hebrev_is_punctuation_impl($string[$cursor])) &&
                $string[$cursor] !== '/' && $string[$cursor] !== '-') {
                $cursor--;
                $block_end--;
            }

            $visual = \substr(
                $string,
                $block_start,
                $block_end - $block_start + 1
            ) . $visual;
            $hebrew_block = true;
        }
        $block_start = $block_end + 1;
    } while ($block_end < $length - 1);

    $begin = $length - 1;
    $end = $begin;
    $result = '';
    while (true) {
        $character_count = 0;
        while (($max_chars_per_line === 0 ||
            ($max_chars_per_line > 0 && $character_count < $max_chars_per_line)) &&
            $begin > 0) {
            $character_count++;
            $begin--;
            if (hebrev_is_newline_impl($visual[$begin])) {
                while ($begin > 0 && hebrev_is_newline_impl($visual[$begin - 1])) {
                    $begin--;
                    $character_count++;
                }
                break;
            }
        }

        if ($max_chars_per_line >= 0 &&
            $character_count === $max_chars_per_line) {
            $new_count = $character_count;
            $new_begin = $begin;
            while ($new_count > 0) {
                if (hebrev_is_blank_impl($visual[$new_begin]) ||
                    hebrev_is_newline_impl($visual[$new_begin])) {
                    break;
                }
                $new_begin++;
                $new_count--;
            }
            if ($new_count > 0) {
                $begin = $new_begin;
            }
        }

        $original_begin = $begin;
        if (hebrev_is_blank_impl($visual[$begin])) {
            $visual[$begin] = "\n";
        }
        while ($begin <= $end && hebrev_is_newline_impl($visual[$begin])) {
            $begin++;
        }
        if ($begin <= $end) {
            $result .= \substr($visual, $begin, $end - $begin + 1);
        }
        for ($index = $original_begin;
            $index <= $end && hebrev_is_newline_impl($visual[$index]);
            $index++) {
            $result .= $visual[$index];
        }

        $begin = $original_begin;
        if ($begin === 0) {
            break;
        }
        $begin--;
        $end = $begin;
    }

    return $result;
}

function hebrev_is_hebrew_impl(string $character): bool
{
    $byte = \ord($character);
    return $byte >= 224 && $byte <= 250;
}

function hebrev_is_blank_impl(string $character): bool
{
    return $character === ' ' || $character === "\t";
}

function hebrev_is_newline_impl(string $character): bool
{
    return $character === "\n" || $character === "\r";
}

function hebrev_is_punctuation_impl(string $character): bool
{
    $byte = \ord($character);
    return ($byte >= 33 && $byte <= 47) ||
        ($byte >= 58 && $byte <= 64) ||
        ($byte >= 91 && $byte <= 96) ||
        ($byte >= 123 && $byte <= 126);
}

function hebrev_mirror_impl(string $character): string
{
    return match ($character) {
        '(' => ')',
        ')' => '(',
        '[' => ']',
        ']' => '[',
        '{' => '}',
        '}' => '{',
        '<' => '>',
        '>' => '<',
        '\\' => '/',
        '/' => '\\',
        default => $character,
    };
}

function parse_str_impl(string $string, &$result): void
{
    $result = [];
    $nul = \strpos($string, "\0");
    if ($nul !== false) {
        $string = \substr($string, 0, $nul);
    }

    $length = \strlen($string);
    $start = 0;
    $count = 0;
    while ($start < $length) {
        while ($start < $length && $string[$start] === '&') {
            $start++;
        }
        if ($start >= $length) {
            break;
        }
        $end = \strpos($string, '&', $start);
        if ($end === false) {
            $end = $length;
        }
        $token = \substr($string, $start, $end - $start);
        $start = $end + 1;

        $count++;
        if ($count > 1000) {
            \PHP2Go\Internal\warning(
                'parse_str(): Input variables exceeded 1000. ' .
                'To increase the limit change max_input_vars in php.ini.'
            );
            break;
        }

        $equals = \strpos($token, '=');
        if ($equals === false) {
            $name = $token;
            $value = '';
        } else {
            $name = \substr($token, 0, $equals);
            $value = url_decode_impl(\substr($token, $equals + 1), true);
        }
        $name = url_decode_impl($name, true);
        query_register_impl($result, $name, $value);
    }
}

function query_register_impl(array &$result, string $name, string $value): void
{
    $nul = \strpos($name, "\0");
    if ($nul !== false) {
        $name = \substr($name, 0, $nul);
    }
    $offset = 0;
    $length = \strlen($name);
    while ($offset < $length && $name[$offset] === ' ') {
        $offset++;
    }
    if ($offset > 0) {
        $name = \substr($name, $offset);
        $length -= $offset;
    }
    if ($length === 0) {
        return;
    }
    $original = $name;

    $root = '';
    $offset = 0;
    while ($offset < $length && $name[$offset] !== '[') {
        $character = $name[$offset];
        $root .= $character === ' ' || $character === '.' ? '_' : $character;
        $offset++;
    }
    if ($root === '') {
        return;
    }
    if ($offset === $length) {
        query_assign_impl($result, [$root], 0, $value, $original);
        return;
    }

    $keys = [$root];
    $nesting = 0;
    while ($offset < $length && $name[$offset] === '[') {
        $nesting++;
        if ($nesting > 64) {
            unset($result[$root]);
            return;
        }

        $bracket = $offset;
        $offset++;
        $index_start = $offset;
        $probe = $offset;
        if ($probe < $length && query_space_impl($name[$probe])) {
            $probe++;
        }
        if ($probe < $length && $name[$probe] === ']') {
            $key = null;
            $close = $probe;
        } else {
            $close = \strpos($name, ']', $probe);
            if ($close === false) {
                if (\count($keys) === 1) {
                    $flat = \substr($name, 0, $bracket) . '_' .
                        \substr($name, $bracket + 1);
                    $flat = \str_replace([' ', '.', '['], '_', $flat);
                    query_assign_impl($result, [$flat], 0, $value, $original);
                } else {
                    query_assign_impl($result, $keys, 0, $value, $original);
                }
                return;
            }
            $key = \substr($name, $index_start, $close - $index_start);
        }
        $keys[] = $key;
        $offset = $close + 1;
        if ($offset >= $length || $name[$offset] !== '[') {
            query_assign_impl($result, $keys, 0, $value, $original);
            return;
        }
    }
}

function query_space_impl(string $character): bool
{
    return $character === ' ' || $character === "\t" ||
        $character === "\n" || $character === "\r" ||
        $character === "\v" || $character === "\f";
}

function query_forbidden_impl(string $key, string $original): bool
{
    foreach (['__Host-', '__Secure-'] as $prefix) {
        if (
            \substr($key, 0, \strlen($prefix)) === $prefix &&
            \substr($original, 0, \strlen($prefix)) !== $prefix
        ) {
            return true;
        }
    }
    return false;
}

function query_assign_impl(
    array &$target,
    array $keys,
    int $offset,
    string $value,
    string $original
): void {
    $key = $keys[$offset];
    if ($key !== null && query_forbidden_impl((string)$key, $original)) {
        return;
    }
    $last = $offset + 1 === \count($keys);
    if ($last) {
        if ($key === null) {
            $target[] = $value;
        } else {
            $target[$key] = $value;
        }
        return;
    }

    if ($key === null) {
        $target[] = [];
        $key = \array_key_last($target);
    } elseif (!\array_key_exists($key, $target) || !\is_array($target[$key])) {
        $target[$key] = [];
    }
    query_assign_impl($target[$key], $keys, $offset + 1, $value, $original);
}

function parse_url_impl(
    string $url,
    int $component = -1
): int|string|array|null|false {
    $parsed = parse_url_parts_impl($url);
    if ($parsed === false) {
        return false;
    }
    [$parts, $has_port] = $parsed;

    if ($component > -1) {
        $names = [
            0 => 'scheme',
            1 => 'host',
            2 => 'port',
            3 => 'user',
            4 => 'pass',
            5 => 'path',
            6 => 'query',
            7 => 'fragment',
        ];
        if (!\array_key_exists($component, $names)) {
            throw new \ValueError(
                'parse_url(): Argument #2 ($component) must be a valid URL ' .
                'component identifier, ' . $component . ' given'
            );
        }
        $name = $names[$component];
        if ($name === 'port' && !$has_port) {
            return null;
        }
        return \array_key_exists($name, $parts) ? $parts[$name] : null;
    }

    $ordered = [];
    foreach (['scheme', 'host', 'port', 'user', 'pass', 'path', 'query', 'fragment'] as $name) {
        if (\array_key_exists($name, $parts)) {
            $ordered[$name] = $parts[$name];
        }
    }
    return $ordered;
}

function get_headers_impl(
    string $url,
    bool $associative = false,
    mixed $context = null
): array|false {
    if ($url === '') {
        throw new \ValueError('Path must not be empty');
    }
    if (\strpos($url, "\0") !== false) {
        throw new \ValueError(
            'get_headers(): Argument #1 ($url) must not contain any null bytes'
        );
    }
    \PHP2Go\Extension\Streams\stream_socket_context_validate_for_open_impl(
        $context,
        'get_headers',
        3,
        'context'
    );

    $separator = \strpos($url, ':');
    $scheme = '';
    if ($separator !== false && $separator > 0) {
        $candidate = \substr($url, 0, $separator);
        $valid_scheme = true;
        for ($index = 0; $index < \strlen($candidate); ++$index) {
            if (!parse_url_is_scheme_impl($candidate[$index])) {
                $valid_scheme = false;
                break;
            }
        }
        if ($valid_scheme) {
            $scheme = \strtolower($candidate);
        }
    }
    if ($scheme !== 'http') {
        if ($scheme === 'data' || $scheme === 'ftp') {
            return false;
        }
        if ($scheme !== '' && $scheme !== 'file' &&
            $scheme !== 'php' && $scheme !== 'glob') {
            \PHP2Go\Internal\warning(
                'get_headers(): Unable to find the wrapper "' . $scheme .
                '" - did you forget to enable it when you configured PHP?'
            );
        }
        \PHP2Go\Internal\warning(
            'get_headers(): This function may only be used against URLs'
        );
        return false;
    }

    if ($context === null) {
        $context = \stream_context_get_default();
    }
    $options = \stream_context_get_options($context);
    $http = \is_array($options['http'] ?? null)
        ? $options['http']
        : [];
    $headers = [];
    $remaining = (int) ($http['max_redirects'] ?? 20);
    $current = $url;
    $redirected = false;
    $keep_method = false;

    while (true) {
        $response = get_headers_http_response_impl(
            $current,
            $context,
            $http,
            $redirected,
            $keep_method
        );
        if ($response === false) {
            return false;
        }
        [$block, $status, $location] = $response;
        foreach ($block as $header) {
            $headers[] = $header;
        }

        $follow = !\array_key_exists('follow_location', $http) ||
            (bool) $http['follow_location'];
        if (!$follow || $status < 300 || $status > 399 || $location === null) {
            break;
        }
        if ($remaining <= 1) {
            break;
        }
        $next = get_headers_redirect_url_impl($current, $location);
        if ($next === false) {
            break;
        }
        --$remaining;
        $current = $next;
        $redirected = true;
        $keep_method = $status === 307 || $status === 308;
    }

    if (!$associative) {
        return $headers;
    }
    return get_headers_associative_impl($headers);
}

function get_headers_http_response_impl(
    string $url,
    mixed $context,
    array $options,
    bool $redirected,
    bool $keep_method
): array|false {
    $parts = parse_url_impl($url);
    if (!\is_array($parts) || !isset($parts['host']) || $parts['host'] === '') {
        \PHP2Go\Internal\warning(
            'get_headers(' . $url . '): Failed to open stream: operation failed'
        );
        return false;
    }
    $host = (string) $parts['host'];
    $port = isset($parts['port']) ? (int) $parts['port'] : 80;
    $address = 'tcp://' . $host . ':' . $port;
    if (\is_string($options['proxy'] ?? null) && $options['proxy'] !== '') {
        $address = $options['proxy'];
    }
    $timeout = isset($options['timeout']) ? (float) $options['timeout'] : 60.0;
    $error_code = 0;
    $error_message = '';
    $socket = @\stream_socket_client(
        $address,
        $error_code,
        $error_message,
        $timeout,
        STREAM_CLIENT_CONNECT,
        $context
    );
    if ($socket === false) {
        if ($error_message === '') {
            $error_message = 'operation failed';
        }
        \PHP2Go\Internal\warning(
            'get_headers(' . $url . '): Failed to open stream: ' .
            $error_message
        );
        return false;
    }

    $path = isset($parts['path']) && $parts['path'] !== ''
        ? (string) $parts['path']
        : '/';
    if (isset($parts['query'])) {
        $path .= '?' . $parts['query'];
    }
    if (!empty($options['request_fulluri'])) {
        $path = $url;
    }
    $method = isset($options['method']) ? (string) $options['method'] : 'GET';
    $version = isset($options['protocol_version'])
        ? \sprintf('%.1F', (float) $options['protocol_version'])
        : '1.1';
    $provided = get_headers_context_lines_impl($options['header'] ?? null);
    if ($redirected && !$keep_method) {
        $filtered = [];
        foreach ($provided as $line) {
            $colon = \strpos($line, ':');
            $name = $colon === false
                ? ''
                : \strtolower(\substr($line, 0, $colon));
            if ($name !== 'content-length' && $name !== 'content-type') {
                $filtered[] = $line;
            }
        }
        $provided = $filtered;
    }
    if ($redirected && !$keep_method && $method !== 'GET' && $method !== 'HEAD') {
        $method = 'GET';
    }
    $content = (!$redirected || $keep_method) &&
        \is_string($options['content'] ?? null) &&
        $options['content'] !== ''
        ? $options['content']
        : null;
    $request = $method . ' ' . $path . ' HTTP/' . $version . "\r\n";
    if (!get_headers_has_request_header_impl($provided, 'host')) {
        $request_host = $host;
        if ($port !== 80) {
            $request_host .= ':' . $port;
        }
        $request .= 'Host: ' . $request_host . "\r\n";
    }
    if (isset($parts['user']) &&
        !get_headers_has_request_header_impl($provided, 'authorization')) {
        $credentials = (string) $parts['user'] . ':' .
            (string) ($parts['pass'] ?? '');
        $request .= 'Authorization: Basic ' .
            \base64_encode($credentials) . "\r\n";
    }
    if (isset($options['user_agent']) &&
        !get_headers_has_request_header_impl($provided, 'user-agent')) {
        $request .= 'User-Agent: ' . (string) $options['user_agent'] . "\r\n";
    }
    foreach ($provided as $line) {
        $request .= $line . "\r\n";
    }
    if ($content !== null &&
        !get_headers_has_request_header_impl($provided, 'content-length')) {
        $request .= 'Content-Length: ' . \strlen($content) . "\r\n";
    }
    if ($content !== null &&
        !get_headers_has_request_header_impl($provided, 'content-type')) {
        \PHP2Go\Internal\notice(
            'get_headers(): Content-type not specified assuming ' .
            'application/x-www-form-urlencoded'
        );
        $request .= "Content-Type: application/x-www-form-urlencoded\r\n";
    }
    if (!get_headers_has_request_header_impl($provided, 'connection')) {
        $request .= "Connection: close\r\n";
    }
    $request .= "\r\n";
    if ($content !== null) {
        $request .= $content;
    }
    if (!get_headers_write_all_impl($socket, $request)) {
        \fclose($socket);
        return false;
    }

    $headers = [];
    $status = 0;
    while (true) {
        $block = [];
        while (($line = \fgets($socket)) !== false) {
            $line = \rtrim($line, "\r\n");
            if ($line === '') {
                break;
            }
            $block[] = $line;
        }
        if ($block === []) {
            break;
        }
        foreach ($block as $line) {
            $headers[] = $line;
        }
        $status = 0;
        $first_space = \strpos($block[0], ' ');
        if ($first_space !== false) {
            $status = (int) \substr($block[0], $first_space + 1, 3);
        }
        if ($status < 100 || $status >= 200) {
            break;
        }
    }
    \fclose($socket);
    if ($headers === []) {
        return false;
    }

    $location = null;
    foreach ($headers as $line) {
        $colon = \strpos($line, ':');
        if ($colon !== false &&
            \strtolower(\substr($line, 0, $colon)) === 'location') {
            $location = \ltrim(\substr($line, $colon + 1));
        }
    }
    return [$headers, $status, $location];
}

function get_headers_write_all_impl(mixed $stream, string $text): bool
{
    $offset = 0;
    $length = \strlen($text);
    while ($offset < $length) {
        $written = \fwrite($stream, \substr($text, $offset));
        if ($written === false || $written === 0) {
            return false;
        }
        $offset += $written;
    }
    return true;
}

function get_headers_context_lines_impl(mixed $headers): array
{
    if (\is_array($headers)) {
        $lines = [];
        foreach ($headers as $line) {
            if (\is_string($line)) {
                $lines[] = \rtrim($line, "\r\n");
            }
        }
        return $lines;
    }
    if ($headers === null || $headers === '') {
        return [];
    }
    $text = \str_replace("\r\n", "\n", (string) $headers);
    $lines = [];
    foreach (\explode("\n", $text) as $line) {
        if ($line !== '') {
            $lines[] = \rtrim($line, "\r");
        }
    }
    return $lines;
}

function get_headers_has_request_header_impl(array $headers, string $name): bool
{
    foreach ($headers as $line) {
        $colon = \strpos($line, ':');
        if ($colon !== false &&
            \strtolower(\substr($line, 0, $colon)) === $name) {
            return true;
        }
    }
    return false;
}

function get_headers_associative_impl(array $headers): array
{
    $result = [];
    foreach ($headers as $header) {
        $colon = \strpos($header, ':');
        if ($colon === false) {
            $result[] = $header;
            continue;
        }
        $name = \substr($header, 0, $colon);
        $value = \substr($header, $colon + 1);
        $offset = 0;
        while ($offset < \strlen($value)) {
            $byte = \ord($value[$offset]);
            if ($byte !== 32 && ($byte < 9 || $byte > 13)) {
                break;
            }
            ++$offset;
        }
        $value = \substr($value, $offset);
        if (!\array_key_exists($name, $result)) {
            $result[$name] = $value;
        } elseif (\is_array($result[$name])) {
            $result[$name][] = $value;
        } else {
            $result[$name] = [$result[$name], $value];
        }
    }
    return $result;
}

function get_headers_redirect_url_impl(string $base, string $location): string|false
{
    if (\strpos($location, '://') !== false) {
        return $location;
    }
    $parts = parse_url_impl($base);
    if (!\is_array($parts) || !isset($parts['scheme'], $parts['host'])) {
        return false;
    }
    $authority = $parts['scheme'] . '://' . $parts['host'];
    if (isset($parts['port'])) {
        $authority .= ':' . $parts['port'];
    }
    if (\substr($location, 0, 2) === '//') {
        return $parts['scheme'] . ':' . $location;
    }
    if (isset($location[0]) && $location[0] === '/') {
        return $authority . $location;
    }
    $path = isset($parts['path']) ? (string) $parts['path'] : '/';
    $slash = \strrpos($path, '/');
    $path = ($slash === false ? '/' : \substr($path, 0, $slash + 1)) .
        $location;
    $segments = [];
    foreach (\explode('/', $path) as $segment) {
        if ($segment === '' || $segment === '.') {
            continue;
        }
        if ($segment === '..') {
            \array_pop($segments);
        } else {
            $segments[] = $segment;
        }
    }
    return $authority . '/' . \implode('/', $segments);
}

function glob_impl(string $pattern, int $flags = 0): array|false
{
    if (\strpos($pattern, "\0") !== false) {
        throw new \ValueError(
            'glob(): Argument #1 ($pattern) must not contain any null bytes'
        );
    }
    if (\strlen($pattern) >= PHP_MAXPATHLEN) {
        \PHP2Go\Internal\warning(
            'glob(): Pattern exceeds the maximum allowed length of ' .
            PHP_MAXPATHLEN . ' characters'
        );
        return false;
    }
    if ((GLOB_AVAILABLE_FLAGS & $flags) !== $flags) {
        \PHP2Go\Internal\warning(
            'glob(): At least one of the passed flags is invalid or not ' .
            'supported on this platform'
        );
        return false;
    }

    [$status, $paths] = \PHP2Go\Internal\glob($pattern, $flags);
    if ($status !== 0) {
        return false;
    }
    if (($flags & GLOB_ONLYDIR) !== 0) {
        $directories = [];
        foreach ($paths as $path) {
            if (\is_dir($path)) {
                $directories[] = $path;
            }
        }
        return $directories;
    }
    return $paths;
}

function parse_url_parts_impl(string $url): array|false
{
    $length = \strlen($url);
    $parts = [];
    $has_port = false;
    $start = 0;
    $colon = \strpos($url, ':');
    $state = 'path';

    if ($colon !== false && $colon !== 0) {
        $valid_scheme = true;
        for ($index = 0; $index < $colon; $index++) {
            if (!parse_url_is_scheme_impl($url[$index])) {
                $valid_scheme = false;
                break;
            }
        }

        if (!$valid_scheme) {
            $special = parse_url_find_first_impl($url, '?#', 0, $length);
            if ($colon + 1 < $length && $colon < $special) {
                $state = 'port';
            } elseif (parse_url_has_slashes_impl($url, $length)) {
                $start = 2;
                $state = 'host';
            }
        } elseif ($colon + 1 === $length) {
            $parts['scheme'] = parse_url_clean_impl(\substr($url, 0, $colon));
            return [$parts, false];
        } elseif ($url[$colon + 1] !== '/') {
            $scan = $colon + 1;
            while ($scan < $length && parse_url_is_digit_impl($url[$scan])) {
                $scan++;
            }
            if (($scan === $length || $url[$scan] === '/') &&
                $scan - $colon < 7) {
                $state = 'port';
            } else {
                $parts['scheme'] = parse_url_clean_impl(
                    \substr($url, 0, $colon)
                );
                $start = $colon + 1;
            }
        } else {
            $parts['scheme'] = parse_url_clean_impl(\substr($url, 0, $colon));
            if ($colon + 2 < $length && $url[$colon + 2] === '/') {
                $start = $colon + 3;
                if (\strtolower($parts['scheme']) === 'file' &&
                    $colon + 3 < $length && $url[$colon + 3] === '/') {
                    if ($colon + 5 < $length && $url[$colon + 5] === ':') {
                        $start = $colon + 4;
                    }
                    $state = 'path';
                } else {
                    $state = 'host';
                }
            } else {
                $start = $colon + 1;
            }
        }
    } elseif ($colon === 0) {
        $state = 'port';
    } elseif (parse_url_has_slashes_impl($url, $length)) {
        $start = 2;
        $state = 'host';
    }

    if ($state === 'port') {
        $port_start = $colon + 1;
        $scan = $port_start;
        while ($scan < $length && $scan - $port_start < 6 &&
            parse_url_is_digit_impl($url[$scan])) {
            $scan++;
        }

        if ($scan - $port_start > 0 && $scan - $port_start < 6 &&
            ($scan === $length || $url[$scan] === '/')) {
            $port = (int) \substr($url, $port_start, $scan - $port_start);
            if ($port < 0 || $port > 65535) {
                return false;
            }
            $parts['port'] = $port;
            $has_port = true;
            if (parse_url_has_slashes_impl($url, $length)) {
                $start = 2;
            }
            $state = 'host';
        } elseif ($scan === $port_start && $scan === $length) {
            return false;
        } elseif (parse_url_has_slashes_impl($url, $length)) {
            $start = 2;
            $state = 'host';
        } else {
            $state = 'path';
        }
    }

    if ($state === 'host') {
        $host_end = parse_url_find_first_impl($url, '/?#', $start, $length);
        $at = parse_url_find_last_impl($url, '@', $start, $host_end);
        if ($at !== false) {
            $separator = parse_url_find_first_impl($url, ':', $start, $at);
            if ($separator < $at) {
                $parts['user'] = parse_url_clean_impl(
                    \substr($url, $start, $separator - $start)
                );
                $parts['pass'] = parse_url_clean_impl(
                    \substr($url, $separator + 1, $at - $separator - 1)
                );
            } else {
                $parts['user'] = parse_url_clean_impl(
                    \substr($url, $start, $at - $start)
                );
            }
            $start = $at + 1;
        }

        if ($start < $length && $url[$start] === '[' &&
            $host_end > $start && $url[$host_end - 1] === ']') {
            $port_separator = false;
        } else {
            $port_separator = parse_url_find_last_impl(
                $url,
                ':',
                $start,
                $host_end
            );
        }

        if ($port_separator !== false) {
            if (($parts['port'] ?? 0) === 0) {
                $port_start = $port_separator + 1;
                $port_length = $host_end - $port_start;
                if ($port_length > 5) {
                    return false;
                }
                if ($port_length > 0) {
                    $parsed_port = parse_url_port_impl(
                        \substr($url, $port_start, $port_length)
                    );
                    if ($parsed_port === false) {
                        return false;
                    }
                    $parts['port'] = $parsed_port;
                    $has_port = true;
                }
            }
            $host_stop = $port_separator;
        } else {
            $host_stop = $host_end;
        }

        if ($host_stop - $start < 1) {
            return false;
        }
        $parts['host'] = parse_url_clean_impl(
            \substr($url, $start, $host_stop - $start)
        );
        if ($host_end === $length) {
            return [$parts, $has_port];
        }
        $start = $host_end;
    }

    $end = $length;
    $fragment = parse_url_find_first_impl($url, '#', $start, $end);
    if ($fragment < $end) {
        $parts['fragment'] = parse_url_clean_impl(
            \substr($url, $fragment + 1, $end - $fragment - 1)
        );
        $end = $fragment;
    }

    $query = parse_url_find_first_impl($url, '?', $start, $end);
    if ($query < $end) {
        $parts['query'] = parse_url_clean_impl(
            \substr($url, $query + 1, $end - $query - 1)
        );
        $end = $query;
    }

    if ($start < $end || $start === $length) {
        $parts['path'] = parse_url_clean_impl(
            \substr($url, $start, $end - $start)
        );
    }
    return [$parts, $has_port];
}

function parse_url_has_slashes_impl(string $url, int $length): bool
{
    return $length > 1 && $url[0] === '/' && $url[1] === '/';
}

function parse_url_is_scheme_impl(string $character): bool
{
    $byte = \ord($character);
    return ($byte >= 65 && $byte <= 90) ||
        ($byte >= 97 && $byte <= 122) ||
        ($byte >= 48 && $byte <= 57) ||
        $character === '+' || $character === '-' || $character === '.';
}

function parse_url_is_digit_impl(string $character): bool
{
    $byte = \ord($character);
    return $byte >= 48 && $byte <= 57;
}

function parse_url_find_first_impl(
    string $url,
    string $characters,
    int $start,
    int $end
): int {
    for ($index = $start; $index < $end; $index++) {
        if (\strpos($characters, $url[$index]) !== false) {
            return $index;
        }
    }
    return $end;
}

function parse_url_find_last_impl(
    string $url,
    string $character,
    int $start,
    int $end
): int|false {
    for ($index = $end - 1; $index >= $start; $index--) {
        if ($url[$index] === $character) {
            return $index;
        }
    }
    return false;
}

function parse_url_port_impl(string $port): int|false
{
    $length = \strlen($port);
    $index = 0;
    while ($index < $length && ini_quantity_is_space_impl($port[$index])) {
        $index++;
    }
    $negative = false;
    if ($index < $length && ($port[$index] === '+' || $port[$index] === '-')) {
        $negative = $port[$index] === '-';
        $index++;
    }
    $digits = 0;
    $value = 0;
    while ($index < $length && parse_url_is_digit_impl($port[$index])) {
        $value = $value * 10 + \ord($port[$index]) - 48;
        $digits++;
        $index++;
    }
    if ($digits === 0) {
        return false;
    }
    if ($negative) {
        $value = -$value;
    }
    return $value >= 0 && $value <= 65535 ? $value : false;
}


function ini_quantity_is_space_impl(string $character): bool
{
    return $character === ' ' || $character === "\t" || $character === "\n" ||
        $character === "\r" || $character === "\v" || $character === "\f";
}
function parse_url_clean_impl(string $value): string
{
    for ($index = 0, $length = \strlen($value); $index < $length; $index++) {
        $byte = \ord($value[$index]);
        if ($byte < 32 || $byte === 127) {
            $value[$index] = '_';
        }
    }
    return $value;
}

function ini_parse_quantity_impl(string $shorthand): int
{
    $length = \strlen($shorthand);
    $digits = 0;
    while ($digits < $length && ini_quantity_is_space_impl($shorthand[$digits])) {
        $digits++;
    }
    $end = $length;
    while ($digits < $end && ini_quantity_is_space_impl($shorthand[$end - 1])) {
        $end--;
    }
    if ($digits === $end) {
        return 0;
    }

    $negative = false;
    if ($shorthand[$digits] === '+') {
        $digits++;
    } elseif ($shorthand[$digits] === '-') {
        $negative = true;
        $digits++;
    }

    if ($digits >= $end || !parse_url_is_digit_impl($shorthand[$digits])) {
        return ini_quantity_warning_impl(
            'Invalid quantity "' . ini_quantity_escape_impl($shorthand) .
            '": no valid leading digits, interpreting as "0" for backwards compatibility',
            0
        );
    }

    $base = 0;
    if ($shorthand[$digits] === '0' &&
        !parse_url_is_digit_impl(ini_quantity_at_impl($shorthand, $digits + 1, $length))) {
        if ($digits + 1 === $end) {
            return 0;
        }

        $prefix = $shorthand[$digits + 1];
        if ($prefix === 'g' || $prefix === 'G' ||
            $prefix === 'm' || $prefix === 'M' ||
            $prefix === 'k' || $prefix === 'K') {
            // A zero followed by a multiplier is evaluated as an ordinary
            // base-zero number rather than as a numeric prefix.
        } elseif ($prefix === 'x' || $prefix === 'X') {
            $base = 16;
            $digits += 2;
        } elseif ($prefix === 'o' || $prefix === 'O') {
            $base = 8;
            $digits += 2;
        } elseif ($prefix === 'b' || $prefix === 'B') {
            $base = 2;
            $digits += 2;
        } else {
            // The public function forwards this errstr through a C "%s".
            // A raw NUL inserted by the "%c" prefix formatter therefore
            // truncates the warning before its closing quote.
            $message = $prefix === "\0"
                ? 'Invalid prefix "0'
                : 'Invalid prefix "0' . $prefix .
                    '", interpreting as "0" for backwards compatibility';
            return ini_quantity_warning_impl($message, 0);
        }

        if ($base !== 0 &&
            ($digits === $end ||
             $digits !== ini_quantity_consume_prefix_impl(
                 $shorthand,
                 $digits,
                 $end,
                 $base
             ))) {
            return ini_quantity_warning_impl(
                'Invalid quantity "' . ini_quantity_escape_impl($shorthand) .
                '": no digits after base prefix, interpreting as "0" for backwards compatibility',
                0
            );
        }
    }

    [$high, $low, $digits_end, $overflow] =
        ini_quantity_parse_unsigned_impl($shorthand, $digits, $end, $base);
    if ($digits_end === $digits) {
        return ini_quantity_warning_impl(
            'Invalid quantity "' . ini_quantity_escape_impl($shorthand) .
            '": no valid leading digits, interpreting as "0" for backwards compatibility',
            0
        );
    }

    if (!$overflow) {
        if ($negative && $high === 2147483648 && $low === 0) {
            // Negating 2^63 leaves the same two's-complement bit pattern.
        } elseif ($high >= 2147483648) {
            $overflow = true;
        } elseif ($negative) {
            [$high, $low] = ini_quantity_negate_impl($high, $low);
        }
    }

    while ($digits_end < $end &&
        ini_quantity_is_space_impl($shorthand[$digits_end])) {
        $digits_end++;
    }

    if ($digits_end === $end) {
        if ($overflow) {
            return ini_quantity_overflow_impl($shorthand, $high, $low);
        }
        return ini_quantity_signed_impl($high, $low);
    }

    $suffix = $shorthand[$end - 1];
    if ($suffix === 'g' || $suffix === 'G') {
        $factor = 1073741824;
    } elseif ($suffix === 'm' || $suffix === 'M') {
        $factor = 1048576;
    } elseif ($suffix === 'k' || $suffix === 'K') {
        $factor = 1024;
    } else {
        $interpreted = ini_quantity_escape_impl(
            \substr($shorthand, 0, $digits_end)
        );
        return ini_quantity_warning_impl(
            'Invalid quantity "' . ini_quantity_escape_impl($shorthand) .
            '": unknown multiplier "' . ini_quantity_escape_impl($suffix) .
            '", interpreting as "' . $interpreted .
            '" for backwards compatibility',
            ini_quantity_signed_impl($high, $low)
        );
    }

    if (!$overflow) {
        $signed = ini_quantity_signed_impl($high, $low);
        if ($signed > 0) {
            $overflow = $signed > \intdiv(PHP_INT_MAX, $factor);
        } elseif ($signed < 0) {
            $overflow = $signed < \intdiv(PHP_INT_MIN, $factor);
        }
    }
    [$high, $low] = ini_quantity_multiply_impl($high, $low, $factor);

    if ($digits_end !== $end - 1) {
        $interpreted = ini_quantity_escape_impl(
            \substr($shorthand, 0, $digits_end)
        );
        return ini_quantity_warning_impl(
            'Invalid quantity "' . ini_quantity_escape_impl($shorthand) .
            '", interpreting as "' . $interpreted .
            ini_quantity_escape_impl($suffix) .
            '" for backwards compatibility',
            ini_quantity_signed_impl($high, $low)
        );
    }

    if ($overflow) {
        return ini_quantity_overflow_impl($shorthand, $high, $low);
    }
    return ini_quantity_signed_impl($high, $low);
}

function ini_quantity_parse_unsigned_impl(
    string $shorthand,
    int $start,
    int $end,
    int $base
): array {
    if ($base === 0) {
        $base = $shorthand[$start] === '0' ? 8 : 10;
    }

    $high = 0;
    $low = 0;
    $overflow = false;
    $index = $start;
    while ($index < $end) {
        $digit = ini_quantity_digit_impl($shorthand[$index]);
        if ($digit < 0 || $digit >= $base) {
            break;
        }

        if (!$overflow) {
            $low_product = $low * $base + $digit;
            $carry = \intdiv($low_product, 4294967296);
            $low = $low_product % 4294967296;
            $high_product = $high * $base + $carry;
            if ($high_product > 4294967295) {
                $overflow = true;
                $high = 4294967295;
                $low = 4294967295;
            } else {
                $high = $high_product;
            }
        }
        $index++;
    }

    return [$high, $low, $index, $overflow];
}

function ini_quantity_consume_prefix_impl(
    string $shorthand,
    int $start,
    int $end,
    int $base
): int {
    $index = $start;
    while ($index < $end && ini_quantity_is_space_impl($shorthand[$index])) {
        $index++;
    }
    if ($index < $end &&
        ($shorthand[$index] === '+' || $shorthand[$index] === '-')) {
        $index++;
    }

    if ($index < $end && $shorthand[$index] === '0' &&
        !parse_url_is_digit_impl(ini_quantity_at_impl(
            $shorthand,
            $index + 1,
            \strlen($shorthand)
        ))) {
        if ($index + 1 === $end) {
            return $index;
        }
        $next = $shorthand[$index + 1];
        if ($next === 'x' || $next === 'X' ||
            $next === 'o' || $next === 'O' ||
            (($next === 'b' || $next === 'B') && $base !== 16)) {
            $index += 2;
        }
    }
    return $index;
}

function ini_quantity_digit_impl(string $character): int
{
    $byte = \ord($character);
    if ($byte >= 48 && $byte <= 57) {
        return $byte - 48;
    }
    if ($byte >= 65 && $byte <= 90) {
        return $byte - 55;
    }
    if ($byte >= 97 && $byte <= 122) {
        return $byte - 87;
    }
    return -1;
}

function ini_quantity_at_impl(string $string, int $index, int $length): string
{
    return $index >= 0 && $index < $length ? $string[$index] : "\0";
}

function ini_quantity_negate_impl(int $high, int $low): array
{
    $low = 4294967295 - $low + 1;
    $carry = 0;
    if ($low >= 4294967296) {
        $low -= 4294967296;
        $carry = 1;
    }
    $high = (4294967295 - $high + $carry) % 4294967296;
    return [$high, $low];
}

function ini_quantity_multiply_impl(int $high, int $low, int $factor): array
{
    $low_product = $low * $factor;
    $carry = \intdiv($low_product, 4294967296);
    $low = $low_product % 4294967296;
    $high = ($high * $factor + $carry) % 4294967296;
    return [$high, $low];
}

function ini_quantity_signed_impl(int $high, int $low): int
{
    if ($high < 2147483648) {
        return $high * 4294967296 + $low;
    }
    if ($high === 2147483648 && $low === 0) {
        return PHP_INT_MIN;
    }

    $magnitude = (4294967295 - $high) * 4294967296 +
        (4294967295 - $low) + 1;
    return -$magnitude;
}

function ini_quantity_overflow_impl(
    string $shorthand,
    int $high,
    int $low
): int {
    return ini_quantity_warning_impl(
        'Invalid quantity "' . ini_quantity_escape_impl($shorthand) .
        '": value is out of range, using overflow result for backwards compatibility',
        ini_quantity_signed_impl($high, $low)
    );
}

function ini_quantity_warning_impl(string $message, int $value): int
{
    \PHP2Go\Internal\warning($message);
    return $value;
}

function ini_quantity_escape_impl(string $string): string
{
    $result = '';
    for ($index = 0, $length = \strlen($string); $index < $length; $index++) {
        $character = $string[$index];
        $byte = \ord($character);
        if ($character === "\n") {
            $result .= '\\n';
        } elseif ($character === "\r") {
            $result .= '\\r';
        } elseif ($character === "\t") {
            $result .= '\\t';
        } elseif ($character === "\f") {
            $result .= '\\f';
        } elseif ($character === "\v") {
            $result .= '\\v';
        } elseif ($byte === 27) {
            $result .= '\\e';
        } elseif ($character === '\\') {
            $result .= '\\\\';
        } elseif ($byte < 32 || $byte > 126) {
            $result .= \sprintf('\\x%02X', $byte);
        } else {
            $result .= $character;
        }
    }
    return $result;
}

function http_build_query_impl(
    array|object $data,
    string $numeric_prefix = '',
    ?string $arg_separator = null,
    int $encoding_type = PHP_QUERY_RFC1738
): string {
    if (\is_object($data) &&
        \enum_exists(\get_class($data), false)) {
        throw new \TypeError(
            'http_build_query(): Argument #1 ($data) must not be an enum, ' .
            \get_class($data) . ' given'
        );
    }

    if ($arg_separator === null) {
        $configured = \ini_get('arg_separator.output');
        $arg_separator = $configured === false ? '&' : (string) $configured;
        if ($arg_separator === '') {
            $arg_separator = '&';
        }
    }

    $trace = \debug_backtrace(\DEBUG_BACKTRACE_IGNORE_ARGS, 2);
    $caller_scope = $trace[1]['class'] ?? null;
    $result = '';
    $stack = [];
    http_build_query_hash_impl(
        $data,
        $numeric_prefix,
        null,
        $arg_separator,
        $encoding_type === \PHP_QUERY_RFC3986,
        $caller_scope,
        $stack,
        $result
    );
    return $result;
}

function http_build_query_hash_impl(
    array|object $data,
    string $numeric_prefix,
    ?string $key_prefix,
    string $arg_separator,
    bool $rfc3986,
    ?string $caller_scope,
    array &$stack,
    string &$result
): void {
    if (\is_array($data)) {
        $identity = 'a:' . \PHP2Go\Internal\array_id($data);
        $values = $data;
        $object_keys = false;
    } else {
        $identity = 'o:' . \spl_object_id($data);
        $values = http_build_query_object_vars_impl($data, $caller_scope);
        $object_keys = true;
    }
    if (isset($stack[$identity])) {
        return;
    }
    $stack[$identity] = true;

    foreach ($values as $key => &$value) {
        if ($object_keys) {
            $segment = http_build_query_encode_impl((string) $key, $rfc3986);
        } elseif (\is_int($key)) {
            $segment = ($key_prefix === null ? $numeric_prefix : '') .
                (string) $key;
        } else {
            $segment = http_build_query_encode_impl($key, $rfc3986);
        }

        if (\is_array($value) ||
            (\is_object($value) &&
             !\enum_exists(\get_class($value), false))) {
            $nested_prefix = $key_prefix === null
                ? $segment . '%5B'
                : $key_prefix . $segment . '%5D%5B';
            http_build_query_hash_impl(
                $value,
                '',
                $nested_prefix,
                $arg_separator,
                $rfc3986,
                $caller_scope,
                $stack,
                $result
            );
            continue;
        }
        if ($value === null || \is_resource($value)) {
            continue;
        }

        $full_key = $key_prefix === null
            ? $segment
            : $key_prefix . $segment . '%5D';
        if ($result !== '') {
            $result .= $arg_separator;
        }
        $result .= $full_key . '=' .
            http_build_query_value_impl($value, $rfc3986);
    }

    unset($stack[$identity]);
    unset($value);
}

function http_build_query_object_vars_impl(
    object $object,
    ?string $caller_scope
): array {
    $reader = static function (object $value): array {
        return \get_object_vars($value);
    };
    if ($caller_scope !== null) {
        $reader = \Closure::bind($reader, null, $caller_scope);
    }
    return $reader($object);
}

function http_build_query_value_impl(mixed $value, bool $rfc3986): string
{
    if (\is_object($value)) {
        if (!($value instanceof \BackedEnum)) {
            throw new \ValueError(
                'Unbacked enum ' . \get_class($value) .
                ' cannot be converted to a string'
            );
        }
        $value = $value->value;
    }
    if (\is_string($value)) {
        return http_build_query_encode_impl($value, $rfc3986);
    }
    if (\is_bool($value)) {
        return $value ? '1' : '0';
    }
    if (\is_float($value)) {
        return http_build_query_encode_impl((string) $value, $rfc3986);
    }
    return (string) $value;
}

function http_build_query_encode_impl(string $value, bool $rfc3986): string
{
    return $rfc3986 ? \rawurlencode($value) : \urlencode($value);
}

function str_ireplace_impl(
    array|string $search,
    array|string $replace,
    string|array $subject,
    mixed &$count = null
): string|array {
    if (\is_string($search) && \is_array($replace)) {
        throw new \TypeError(
            'str_ireplace(): Argument #2 ($replace) must be of type string ' .
            'when argument #1 ($search) is a string'
        );
    }

    $total = 0;
    if (\is_array($subject)) {
        $result = [];
        foreach ($subject as $key => $value) {
            $result[$key] = str_ireplace_subject_impl(
                $search,
                $replace,
                (string) $value,
                $total
            );
        }
        $count = $total;
        return $result;
    }

    $result = str_ireplace_subject_impl($search, $replace, $subject, $total);
    $count = $total;
    return $result;
}

function str_ireplace_subject_impl(
    array|string $search,
    array|string $replace,
    string $subject,
    int &$count
): string {
    if ($subject === '') {
        return '';
    }

    if (\is_string($search)) {
        return str_ireplace_one_impl($subject, $search, $replace, $count);
    }

    $replacement_values = \is_array($replace) ? \array_values($replace) : null;
    $replacement_index = 0;
    foreach ($search as $needle_value) {
        $needle = (string) $needle_value;
        if ($replacement_values === null) {
            $replacement = $replace;
        } elseif ($replacement_index < \count($replacement_values)) {
            $replacement = (string) $replacement_values[$replacement_index];
            $replacement_index++;
        } else {
            $replacement = '';
        }

        if ($needle !== '') {
            $subject = str_ireplace_one_impl(
                $subject,
                $needle,
                $replacement,
                $count
            );
            if ($subject === '') {
                return '';
            }
        }
    }
    return $subject;
}

function str_ireplace_one_impl(
    string $subject,
    string $needle,
    string $replacement,
    int &$count
): string {
    if ($needle === '') {
        return $subject;
    }

    $folded_subject = \strtolower($subject);
    $folded_needle = \strtolower($needle);
    $needle_length = \strlen($needle);
    $offset = 0;
    $result = '';

    while (($match = \strpos($folded_subject, $folded_needle, $offset)) !== false) {
        $result .= \substr($subject, $offset, $match - $offset) . $replacement;
        $offset = $match + $needle_length;
        $count++;
    }
    if ($offset === 0) {
        return $subject;
    }
    return $result . \substr($subject, $offset);
}

function get_include_path_impl(): string|false
{
    return \ini_get('include_path');
}


function set_include_path_impl(string $include_path): string|false
{
    if (\strpos($include_path, "\0") !== false) {
        throw new \ValueError(
            'set_include_path(): Argument #1 ($include_path) must not contain any null bytes'
        );
    }

    return \ini_set('include_path', $include_path);
}

function password_algos_impl(): array
{
    // The linked engine is built without Argon2. Its password registry contains
    // only bcrypt, whose registry identifier is the public PASSWORD_BCRYPT value.
    return ['2y'];
}

function password_get_info_impl(string $hash): array
{
    if (\strlen($hash) !== 60 || \substr($hash, 0, 4) !== '$2y$') {
        return [
            'algo' => null,
            'algoName' => 'unknown',
            'options' => [],
        ];
    }

    return [
        'algo' => '2y',
        'algoName' => 'bcrypt',
        'options' => ['cost' => password_bcrypt_cost_impl($hash)],
    ];
}

function password_hash_impl(mixed $password, mixed $algo, mixed $options = []): string
{
    $password = (string) $password;
    if ($algo !== null) {
        $algo = \is_string($algo) || \is_object($algo)
            ? (string) $algo
            : (int) $algo;
    }
    if ($algo !== null && $algo !== 0 && $algo !== 1 && $algo !== '2y') {
        throw new \ValueError(
            'password_hash(): Argument #2 ($algo) must be a valid password hashing algorithm'
        );
    }

    if (\strpos($password, "\0") !== false) {
        throw new \ValueError('Bcrypt password must not contain null character');
    }

    $cost = \array_key_exists('cost', $options)
        ? (int) $options['cost']
        : 12;
    if ($cost < 4 || $cost > 31) {
        throw new \ValueError('Invalid bcrypt cost parameter specified: ' . $cost);
    }

    if (\array_key_exists('salt', $options)) {
        \PHP2Go\Internal\warning(
            'password_hash(): The "salt" option has been ignored, since providing a custom salt is no longer supported'
        );
    }

    $salt = \PHP2Go\Internal\password_salt();
    if ($salt === false) {
        throw new \ValueError('Unable to generate salt');
    }
    $result = \PHP2Go\Internal\crypt(
        $password,
        '$2y$' . \sprintf('%02d', $cost) . '$' . $salt
    );
    if (\strlen($result) < 13) {
        throw new \Error('Password hashing failed for unknown reason');
    }
    return $result;
}

function password_needs_rehash_impl(
    string $hash,
    string|int|null $algo,
    array $options = []
): bool {
    if ($algo !== null && $algo !== 0 && $algo !== 1 && $algo !== '2y') {
        return false;
    }

    $info = password_get_info_impl($hash);
    if ($info['algo'] !== '2y') {
        return true;
    }

    $cost = \array_key_exists('cost', $options)
        ? (int) $options['cost']
        : 12;
    return $info['options']['cost'] !== $cost;
}

function password_verify_impl(mixed $password, mixed $hash): bool
{
    return \PHP2Go\Internal\password_verify((string) $password, (string) $hash);
}

function password_bcrypt_cost_impl(string $hash): int
{
    $index = 4;
    $length = \strlen($hash);
    while ($index < $length) {
        $byte = \ord($hash[$index]);
        if ($byte !== 9 && ($byte < 10 || $byte > 13) && $byte !== 32) {
            break;
        }
        $index++;
    }

    $negative = false;
    if ($index < $length && ($hash[$index] === '+' || $hash[$index] === '-')) {
        $negative = $hash[$index] === '-';
        $index++;
    }

    $result = 0;
    $digits = 0;
    while ($index < $length) {
        $digit = \ord($hash[$index]) - 48;
        if ($digit < 0 || $digit > 9) {
            break;
        }
        $digits++;
        if ($negative) {
            if ($result < \intdiv(PHP_INT_MIN + $digit, 10)) {
                return PHP_INT_MIN;
            }
            $result = $result * 10 - $digit;
        } else {
            if ($result > \intdiv(PHP_INT_MAX - $digit, 10)) {
                return PHP_INT_MAX;
            }
            $result = $result * 10 + $digit;
        }
        $index++;
    }

    return $digits === 0 ? 12 : $result;
}
function strtok_impl(string $string, ?string $token = null): string|false
{
    static $state = null;
    static $offset = 0;

    if ($token === null) {
        $token = $string;
    } else {
        $state = $string;
        $offset = 0;
    }

    if ($state === null) {
        \PHP2Go\Internal\warning(
            'strtok(): Both arguments must be provided when starting tokenization'
        );
        return false;
    }

    $length = \strlen($state);
    if ($offset >= $length) {
        return false;
    }

    while ($offset < $length &&
           \strpos($token, $state[$offset]) !== false) {
        $offset++;
    }
    if ($offset >= $length) {
        $state = null;
        $offset = 0;
        return false;
    }

    $start = $offset++;
    while ($offset < $length &&
           \strpos($token, $state[$offset]) === false) {
        $offset++;
    }
    $result = \substr($state, $start, $offset - $start);
    $offset++;
    return $result;
}

// This body uses only language/runtime primitives already implemented by
// php2go. Its public builtin name and signature are supplied by bundle metadata
// and the harvested engine manifest respectively.
function is_countable_impl(mixed $value): bool {
    return \is_array($value) || $value instanceof \Countable;
}

function is_iterable_impl(mixed $value): bool
{
    return \is_array($value) || $value instanceof \Traversable;
}

function parse_ini_string_impl(
    string $ini_string,
    bool $process_sections = false,
    int $scanner_mode = INI_SCANNER_NORMAL
): array|false {
    return ini_parse_document_impl(
        $ini_string,
        $process_sections,
        $scanner_mode,
        'Unknown'
    );
}

function parse_ini_file_impl(
    string $filename,
    bool $process_sections = false,
    int $scanner_mode = INI_SCANNER_NORMAL
): array|false {
    if ($filename === '') {
        throw new \ValueError(
            'parse_ini_file(): Argument #1 ($filename) must not be empty'
        );
    }
    if (\strpos($filename, "\0") !== false) {
        throw new \ValueError(
            'parse_ini_file(): Argument #1 ($filename) must not contain any null bytes'
        );
    }

    $contents = \PHP2Go\Internal\read_file($filename);
    if ($contents === false) {
        \PHP2Go\Internal\warning(
            'parse_ini_file(' . $filename .
            '): Failed to open stream: No such file or directory'
        );
        return false;
    }

    return ini_parse_document_impl(
        $contents,
        $process_sections,
        $scanner_mode,
        $filename
    );
}

function ini_parse_document_impl(
    string $source,
    bool $process_sections,
    int $scanner_mode,
    string $filename
): array|false {
    if ($scanner_mode !== INI_SCANNER_NORMAL &&
        $scanner_mode !== INI_SCANNER_RAW &&
        $scanner_mode !== INI_SCANNER_TYPED) {
        \PHP2Go\Internal\warning('Invalid scanner mode');
        return false;
    }

    $nul = \strpos($source, "\0");
    if ($nul !== false) {
        $source = \substr($source, 0, $nul);
    }
    if (\strlen($source) > 3 && \substr($source, 0, 3) === "\xEF\xBB\xBF") {
        $source = \substr($source, 3);
    }

    $state = [
        'source' => $source,
        'length' => \strlen($source),
        'offset' => 0,
        'line' => 1,
        'filename' => $filename,
        'unexpected' => null,
        'expected' => null,
        'error_line' => 1,
    ];
    $result = [];
    $active = false;
    $active_key = null;

    while ($state['offset'] < $state['length']) {
        [$statement, $line, $terminated] =
            ini_next_statement_impl($state, $scanner_mode);
        if (ini_trim_space_impl($statement) === '') {
            continue;
        }
        if (!ini_parse_statement_impl(
            $statement,
            $line,
            $terminated,
            $scanner_mode,
            $process_sections,
            $result,
            $active,
            $active_key,
            $state
        )) {
            ini_syntax_warning_impl($state);
            return false;
        }
    }

    return $result;
}

function ini_next_statement_impl(array &$state, int $scanner_mode): array
{
    $source = $state['source'];
    $length = $state['length'];
    $start = $state['offset'];
    $line = $state['line'];
    $quote = '';
    $index = $start;
    $has_equal = false;

    while ($index < $length) {
        $character = $source[$index];

        if ($scanner_mode !== INI_SCANNER_RAW && $quote !== '') {
            if ($character === "\r" || $character === "\n") {
                if ($character === "\r" &&
                    $index + 1 < $length &&
                    $source[$index + 1] === "\n") {
                    $index += 2;
                } else {
                    $index++;
                }
                $state['line']++;
                continue;
            }
            if ($quote === '"' && $character === '\\' &&
                $index + 1 < $length) {
                if ($source[$index + 1] === '"' &&
                    ($index + 2 >= $length ||
                     $source[$index + 2] === "\r" ||
                     $source[$index + 2] === "\n")) {
                    $index += 2;
                    $quote = '';
                    continue;
                }
                $index += 2;
                continue;
            }
            if ($character === $quote) {
                $quote = '';
            }
            $index++;
            continue;
        }

        if ($scanner_mode !== INI_SCANNER_RAW &&
            ($character === '"' || $character === "'")) {
            $quote = $character;
            $index++;
            continue;
        }

        if ($character === '=') {
            $has_equal = true;
        }
        if ($character === ';' &&
            ($scanner_mode !== INI_SCANNER_RAW ||
             !$has_equal)) {
            $statement = \substr($source, $start, $index - $start);
            while ($index < $length &&
                   $source[$index] !== "\r" &&
                   $source[$index] !== "\n") {
                $index++;
            }
            if ($index < $length) {
                if ($source[$index] === "\r" &&
                    $index + 1 < $length &&
                    $source[$index + 1] === "\n") {
                    $index += 2;
                } else {
                    $index++;
                }
                $state['line']++;
            }
            $state['offset'] = $index;
            return [$statement, $line, true];
        }

        if ($character === "\r" || $character === "\n") {
            $statement = \substr($source, $start, $index - $start);
            if ($character === "\r" &&
                $index + 1 < $length &&
                $source[$index + 1] === "\n") {
                $index += 2;
            } else {
                $index++;
            }
            $state['line']++;
            $state['offset'] = $index;
            return [$statement, $line, true];
        }
        $index++;
    }

    $state['offset'] = $length;
    return [\substr($source, $start), $line, false];
}

function ini_parse_statement_impl(
    string $statement,
    int $line,
    bool $terminated,
    int $scanner_mode,
    bool $process_sections,
    array &$result,
    bool &$active,
    mixed &$active_key,
    array &$state
): bool {
    $statement = ini_ltrim_space_impl($statement);
    $state['error_line'] = $line;

    if ($statement !== '' && $statement[0] === '[') {
        $end = ini_section_end_impl($statement);
        if ($end === false) {
            return ini_unterminated_section_impl(
                \substr($statement, 1),
                $line,
                $state
            );
        }
        if (ini_trim_space_impl(\substr($statement, $end + 1)) !== '') {
            return ini_error_impl($state, 'TC_STRING', null, $line);
        }

        $section_text = \substr($statement, 1, $end - 1);
        $section = $section_text;
        if ($scanner_mode !== INI_SCANNER_RAW) {
            $index = 0;
            $ok = true;
            [$section] = ini_concat_impl(
                $section_text,
                $index,
                $scanner_mode,
                false,
                $state,
                $ok
            );
            if (!$ok || $index !== \strlen($section_text)) {
                return false;
            }
        }
        if ($process_sections) {
            $result[$section] = [];
            $active = true;
            $active_key = $section;
        }
        return true;
    }

    $equal = \strpos($statement, '=');
    if ($equal === false) {
        $label = ini_trim_space_impl($statement);
        return $label === '' ||
            ini_validate_label_impl($label, $state, $line);
    }

    $left = ini_trim_space_impl(\substr($statement, 0, $equal));
    if ($left === '') {
        return ini_error_impl($state, "'='", null, $line);
    }

    $offset = null;
    $bracket = \strpos($left, '[');
    if ($bracket !== false) {
        if ($left[\strlen($left) - 1] !== ']') {
            return ini_error_impl($state, 'TC_SECTION', "']'", $line);
        }
        $key = ini_rtrim_space_impl(\substr($left, 0, $bracket));
        $offset_text = \substr(
            $left,
            $bracket + 1,
            \strlen($left) - $bracket - 2
        );
        if (\strpos($offset_text, '[') !== false ||
            \strpos($offset_text, ']') !== false) {
            return ini_error_impl($state, 'TC_SECTION', "'='", $line);
        }
        if (!ini_validate_label_impl($key, $state, $line)) {
            return false;
        }
        if ($offset_text === '') {
            $offset = '';
        } else {
            $offset_index = 0;
            $ok = true;
            [$offset] = ini_concat_impl(
                $offset_text,
                $offset_index,
                INI_SCANNER_NORMAL,
                true,
                $state,
                $ok
            );
            if (!$ok || $offset_index !== \strlen($offset_text)) {
                return false;
            }
        }
    } else {
        $key = $left;
        if (!ini_validate_label_impl($key, $state, $line)) {
            return false;
        }
    }

    $value_text = ini_ltrim_space_impl(\substr($statement, $equal + 1));
    if ($terminated) {
        $value_text = ini_rtrim_space_impl($value_text);
    } elseif (ini_trim_space_impl($value_text) === '') {
        $value_text = '';
    }

    $ok = true;
    $value = $scanner_mode === INI_SCANNER_RAW
        ? ini_raw_value_impl($value_text)
        : ini_value_impl($value_text, $scanner_mode, $state, $ok);
    if (!$ok) {
        return false;
    }

    if ($process_sections && $active) {
        $target =& $result[$active_key];
    } else {
        $target =& $result;
    }

    if ($offset === null) {
        $target[$key] = $value;
    } else {
        if (!isset($target[$key]) || !\is_array($target[$key])) {
            $target[$key] = [];
        }
        if ($offset === '') {
            $target[$key][] = $value;
        } else {
            $target[$key][$offset] = $value;
        }
    }
    return true;
}

// An unterminated section is not refused as a whole: the engine's parser
// was part-way through its tokens when the input ended, and the expectation
// it reports is the innermost one still open -- `[${zz:-x` expects '}', and
// `["abc` the rest of a quoted string. Only when every token closed does it
// expect ']'. Its tokens stop at the first line end outside double quotes
// (a later `"b` or `${` is not read) and at a `;` comment; an unclosed `'`
// swallows the rest of the line; `|` and `=` are ordinary characters here.
// The ']' is expected on the line where the section's text ended.
function ini_unterminated_section_impl(
    string $text,
    int $line,
    array &$state
): false {
    $length = \strlen($text);
    $end = 0;
    $quoted = false;
    while ($end < $length) {
        $character = $text[$end];
        if ($quoted) {
            if ($character === '\\' && $end + 1 < $length) {
                $end += 2;
                continue;
            }
            if ($character === '"') {
                $quoted = false;
            }
        } elseif ($character === '"') {
            $quoted = true;
        } elseif ($character === "\n" || $character === "\r") {
            break;
        }
        $end++;
    }
    $text = \substr($text, 0, $end);
    $length = $end;

    $index = 0;
    while ($index < $length) {
        $character = $text[$index];
        if ($character === ';') {
            break;
        }
        if ($character === "'") {
            $close = \strpos($text, "'", $index + 1);
            if ($close === false) {
                break;
            }
            $index = $close + 1;
            continue;
        }
        if ($character === '"') {
            $ok = true;
            ini_quoted_impl($text, $index, $state, $ok);
            if (!$ok) {
                return false;
            }
            continue;
        }
        if ($character === '$' && $index + 1 < $length &&
            $text[$index + 1] === '{') {
            $ok = true;
            ini_variable_impl(
                $text,
                $index,
                INI_SCANNER_NORMAL,
                $state,
                $ok
            );
            if (!$ok) {
                return false;
            }
            continue;
        }
        $index++;
    }
    return ini_error_impl(
        $state,
        'end of file',
        "']'",
        $line + ini_newline_count_impl($text)
    );
}

function ini_section_end_impl(string $statement): int|false
{
    $quote = '';
    for ($index = 1, $length = \strlen($statement);
         $index < $length;
         $index++) {
        $character = $statement[$index];
        if ($quote !== '') {
            if ($quote === '"' && $character === '\\' &&
                $index + 1 < $length) {
                $index++;
            } elseif ($character === $quote) {
                $quote = '';
            }
        } elseif ($character === '"' || $character === "'") {
            $quote = $character;
        } elseif ($character === ']') {
            return $index;
        }
    }
    return false;
}

function ini_validate_label_impl(
    string $label,
    array &$state,
    int $line
): bool {
    if ($label === '') {
        return ini_error_impl($state, "'='", null, $line);
    }
    $reserved = [
        'true' => 'BOOL_TRUE',
        'on' => 'BOOL_TRUE',
        'yes' => 'BOOL_TRUE',
        'false' => 'BOOL_FALSE',
        'off' => 'BOOL_FALSE',
        'no' => 'BOOL_FALSE',
        'none' => 'BOOL_FALSE',
        'null' => 'NULL_NULL',
    ];
    if (isset($reserved[$label])) {
        return ini_error_impl($state, $reserved[$label], null, $line);
    }

    $forbidden = '=;&|^$~(){}!"[]';
    for ($index = 0, $length = \strlen($label);
         $index < $length;
         $index++) {
        $character = $label[$index];
        if ($character === "\t" || $character === "\0" ||
            \strpos($forbidden, $character) !== false) {
            return ini_error_impl(
                $state,
                "'" . $character . "'",
                null,
                $line
            );
        }
    }
    return true;
}

function ini_raw_value_impl(string $value): string
{
    $value = ini_ltrim_space_impl($value);
    $comment = null;
    $quoted = $value !== '' && $value[0] === '"';
    for ($index = 0, $length = \strlen($value);
         $index < $length;
         $index++) {
        if ($value[$index] === ';' && $comment === null) {
            $comment = $index;
        } elseif ($quoted && $value[$index] === '"') {
            $comment = null;
        }
    }
    if ($comment !== null) {
        $value = \substr($value, 0, $comment);
    }
    $value = ini_rtrim_space_impl($value);
    $length = \strlen($value);
    if ($length > 1 && $value[0] === '"' &&
        $value[$length - 1] === '"') {
        return \substr($value, 1, $length - 2);
    }
    return $value;
}

function ini_value_impl(
    string $text,
    int $scanner_mode,
    array &$state,
    bool &$ok
): mixed {
    if ($text === '') {
        return '';
    }

    $keyword = ini_rtrim_space_impl($text);
    $booleans = [
        'true' => true,
        'on' => true,
        'yes' => true,
        'false' => false,
        'off' => false,
        'no' => false,
        'none' => false,
        'null' => null,
    ];
    if (\array_key_exists($keyword, $booleans)) {
        $value = $booleans[$keyword];
        return $scanner_mode === INI_SCANNER_TYPED
            ? $value
            : ($value === true ? '1' : '');
    }

    $index = 0;
    [$value, $numeric] = ini_expr_impl(
        $text,
        $index,
        $scanner_mode,
        $state,
        $ok
    );
    if (!$ok) {
        return '';
    }
    if ($index !== \strlen($text)) {
        $unexpected = $text[$index] === '='
            ? "'='"
            : "'" . $text[$index] . "'";
        $ok = ini_error_impl(
            $state,
            $unexpected,
            null,
            $state['error_line']
        );
        return '';
    }
    return $scanner_mode === INI_SCANNER_TYPED && $numeric
        ? ini_typed_number_impl((string) $value)
        : $value;
}

function ini_expr_impl(
    string $text,
    int &$index,
    int $scanner_mode,
    array &$state,
    bool &$ok
): array {
    [$value, $numeric] = ini_expr_atom_impl(
        $text,
        $index,
        $scanner_mode,
        $state,
        $ok
    );
    if (!$ok) {
        return ['', false];
    }

    $length = \strlen($text);
    while ($index < $length &&
           ($text[$index] === '|' ||
            $text[$index] === '&' ||
            $text[$index] === '^')) {
        $operator = $text[$index++];
        [$right] = ini_expr_atom_impl(
            $text,
            $index,
            $scanner_mode,
            $state,
            $ok
        );
        if (!$ok) {
            return ['', false];
        }
        $left_int = ini_int32_impl((int) $value);
        $right_int = ini_int32_impl((int) $right);
        $value = match ($operator) {
            '|' => ini_int32_impl($left_int | $right_int),
            '&' => ini_int32_impl($left_int & $right_int),
            default => ini_int32_impl($left_int ^ $right_int),
        };
        $numeric = true;
    }
    if ($scanner_mode !== INI_SCANNER_TYPED &&
        $numeric && \is_int($value)) {
        $value = (string) $value;
    }
    return [$value, $numeric];
}

function ini_expr_atom_impl(
    string $text,
    int &$index,
    int $scanner_mode,
    array &$state,
    bool &$ok
): array {
    $length = \strlen($text);
    if ($index >= $length) {
        $ok = ini_error_impl(
            $state,
            'end of file',
            'TC_CONSTANT or TC_NUMBER or TC_STRING',
            $state['error_line']
        );
        return ['', false];
    }

    $character = $text[$index];
    if ($character === '~' || $character === '!') {
        $operator = $character;
        $index++;
        [$value] = ini_expr_atom_impl(
            $text,
            $index,
            $scanner_mode,
            $state,
            $ok
        );
        if (!$ok) {
            return ['', false];
        }
        $integer = ini_int32_impl((int) $value);
        $value = $operator === '~'
            ? ini_int32_impl(~$integer)
            : ($integer === 0 ? 1 : 0);
        return [
            $scanner_mode === INI_SCANNER_TYPED
                ? $value
                : (string) $value,
            true,
        ];
    }

    if ($character === '(') {
        $index++;
        [$value, $numeric] = ini_expr_impl(
            $text,
            $index,
            $scanner_mode,
            $state,
            $ok
        );
        if (!$ok) {
            return ['', false];
        }
        if ($index >= $length || $text[$index] !== ')') {
            $ok = ini_error_impl(
                $state,
                'end of file',
                "')'",
                $state['error_line']
            );
            return ['', false];
        }
        $index++;
        return [$value, $numeric];
    }

    return ini_concat_impl(
        $text,
        $index,
        $scanner_mode,
        true,
        $state,
        $ok
    );
}

function ini_concat_impl(
    string $text,
    int &$index,
    int $scanner_mode,
    bool $resolve_constants,
    array &$state,
    bool &$ok,
    bool $operators_literal = false
): array {
    $length = \strlen($text);
    $result = '';
    $pieces = 0;
    $numeric = false;

    while ($index < $length) {
        $character = $text[$index];
        // A `${name:-fallback}` fallback is not an expression: `x|y` and
        // `x=y` there are the text they spell.
        if (!$operators_literal && (
            $character === '|' || $character === '&' ||
            $character === '^' || $character === '~' ||
            $character === '!' || $character === '(' ||
            $character === ')' || $character === '=')) {
            break;
        }

        if ($character === '"') {
            $result .= ini_quoted_impl($text, $index, $state, $ok);
            if (!$ok) {
                return ['', false];
            }
            $pieces++;
            $numeric = false;
            continue;
        }

        // A fallback's unquoted text is one piece up to the next quoted
        // string or variable, spaces and all, and it is a constant only
        // when the whole piece names one: `${X:-true}` is "1", while
        // `${X:-x true}` and `${X:- true}` are the text as written.
        if ($operators_literal &&
            !($character === '$' && $index + 1 < $length &&
                $text[$index + 1] === '{')) {
            $start = $index;
            while ($index < $length && $text[$index] !== '"' &&
                   !($text[$index] === '$' && $index + 1 < $length &&
                       $text[$index + 1] === '{')) {
                $index++;
            }
            $piece = \substr($text, $start, $index - $start);
            if ($resolve_constants && ini_constant_name_impl($piece) &&
                \defined($piece)) {
                $constant = \constant($piece);
                if ($constant === true) {
                    $piece = '1';
                } elseif ($constant === false || $constant === null) {
                    $piece = '';
                } else {
                    $piece = (string) $constant;
                }
            }
            $result .= $piece;
            $pieces++;
            $numeric = false;
            continue;
        }

        if ($character === "'") {
            $end = \strpos($text, "'", $index + 1);
            if ($end === false) {
                $ok = ini_error_impl(
                    $state,
                    'end of file',
                    "TC_RAW or \"'\"",
                    $state['error_line']
                );
                return ['', false];
            }
            $result .= \substr(
                $text,
                $index + 1,
                $end - $index - 1
            );
            $index = $end + 1;
            $pieces++;
            $numeric = false;
            continue;
        }

        if ($character === '$' && $index + 1 < $length &&
            $text[$index + 1] === '{') {
            $result .= ini_variable_impl(
                $text,
                $index,
                $scanner_mode,
                $state,
                $ok
            );
            if (!$ok) {
                return ['', false];
            }
            $pieces++;
            $numeric = false;
            continue;
        }

        $number_length = ini_number_length_impl($text, $index);
        if ($number_length > 0) {
            $result .= \substr($text, $index, $number_length);
            $index += $number_length;
            $pieces++;
            $numeric = $pieces === 1;
            continue;
        }

        if (ini_constant_start_impl($character)) {
            $start = $index++;
            while ($index < $length &&
                   ini_constant_continue_impl($text[$index])) {
                $index++;
            }
            $piece = \substr($text, $start, $index - $start);
            $token = ini_reserved_token_impl($piece);
            if ($token !== null) {
                $ok = ini_error_impl(
                    $state,
                    $token,
                    null,
                    $state['error_line']
                );
                return ['', false];
            }
            if ($resolve_constants && \defined($piece)) {
                $constant = \constant($piece);
                if ($constant === true) {
                    $piece = '1';
                } elseif ($constant === false || $constant === null) {
                    $piece = '';
                } else {
                    $piece = (string) $constant;
                }
            }
            $result .= $piece;
            $pieces++;
            $numeric = false;
            continue;
        }

        $result .= $character;
        $index++;
        $pieces++;
        $numeric = false;
    }

    if ($pieces === 0) {
        $unexpected = $index < $length
            ? "'" . $text[$index] . "'"
            : 'end of file';
        $ok = ini_error_impl(
            $state,
            $unexpected,
            'TC_CONSTANT or TC_NUMBER or TC_STRING',
            $state['error_line']
        );
        return ['', false];
    }
    return [$result, $numeric];
}

function ini_constant_name_impl(string $piece): bool
{
    $length = \strlen($piece);
    if ($length === 0 || !ini_constant_start_impl($piece[0])) {
        return false;
    }
    for ($index = 1; $index < $length; $index++) {
        if (!ini_constant_continue_impl($piece[$index])) {
            return false;
        }
    }
    return true;
}

function ini_quoted_impl(
    string $text,
    int &$index,
    array &$state,
    bool &$ok
): string {
    $length = \strlen($text);
    $index++;
    $result = '';

    while ($index < $length) {
        $character = $text[$index];
        if ($character === '"') {
            $index++;
            return $result;
        }
        if ($character === '\\') {
            if ($index + 1 >= $length) {
                $result .= '\\';
                $index++;
                continue;
            }
            $next = $text[$index + 1];
            if ($next === '"' && $index + 2 >= $length) {
                $result .= '\\';
                $index += 2;
                return $result;
            }
            $result .= $next === '"' ||
                $next === '\\' ||
                $next === '$'
                ? $next
                : '\\' . $next;
            $index += 2;
            continue;
        }
        if ($character === '$' && $index + 1 < $length &&
            $text[$index + 1] === '{') {
            $result .= ini_variable_impl(
                $text,
                $index,
                INI_SCANNER_NORMAL,
                $state,
                $ok
            );
            if (!$ok) {
                return '';
            }
            continue;
        }
        $result .= $character;
        $index++;
    }

    $ok = ini_error_impl(
        $state,
        'end of file',
        'TC_DOLLAR_CURLY or TC_QUOTED_STRING or ' .
            \chr(39) . '"' . \chr(39),
        $state['error_line'] + ini_newline_count_impl($text)
    );
    return '';
}

function ini_variable_impl(
    string $text,
    int &$index,
    int $scanner_mode,
    array &$state,
    bool &$ok
): string {
    $length = \strlen($text);
    $index += 2;
    $name_start = $index;
    $fallback_at = null;

    while ($index < $length) {
        if ($text[$index] === '}') {
            break;
        }
        if ($text[$index] === ':' && $index + 1 < $length &&
            $text[$index + 1] === '-') {
            $fallback_at = $index;
            break;
        }
        $index++;
    }
    if ($index >= $length) {
        // Nothing at all after `${` is a missing name; a name (or only
        // spaces) is waiting for its fallback or its close.
        $ok = ini_error_impl(
            $state,
            'end of file',
            $name_start >= $length ? 'TC_VARNAME' : "TC_FALLBACK or '}'",
            $state['error_line']
        );
        return '';
    }

    $name = ini_trim_space_impl(
        \substr($text, $name_start, $index - $name_start)
    );
    if ($name === '') {
        $ok = ini_error_impl(
            $state,
            $fallback_at === null ? "'}'" : 'TC_FALLBACK',
            'TC_VARNAME',
            $state['error_line']
        );
        return '';
    }

    $fallback = null;
    if ($fallback_at !== null) {
        $index += 2;
        $fallback_start = $index;
        $quote = '';
        // A fallback may hold a variable of its own -- `${a:-${b}}` -- whose
        // '}' is not the outer one's.
        $nested = 0;
        $nested_at = 0;
        while ($index < $length) {
            $character = $text[$index];
            if ($quote === '' && $character === '$' &&
                $index + 1 < $length && $text[$index + 1] === '{') {
                $nested++;
                $index += 2;
                $nested_at = $index;
                continue;
            }
            if ($quote === '' && $nested > 0 && $character === '}') {
                $nested--;
                $index++;
                continue;
            }
            if ($quote !== '') {
                if ($quote === '"' && $character === '\\' &&
                    $index + 1 < $length) {
                    $index += 2;
                    continue;
                }
                if ($character === $quote) {
                    $quote = '';
                }
                $index++;
                continue;
            }
            if ($character === '"' || $character === "'") {
                $quote = $character;
                $index++;
                continue;
            }
            if ($character === '}') {
                break;
            }
            $index++;
        }
        if ($index >= $length) {
            // The innermost construct still open is the one the engine
            // names: an unfinished quoted string, then an unfinished
            // inner variable, then this one's '}'.
            if ($quote === '"') {
                $expected = 'TC_DOLLAR_CURLY or TC_QUOTED_STRING or ' .
                    \chr(39) . '"' . \chr(39);
                $error_line = $state['error_line'] +
                    ini_newline_count_impl($text);
            } elseif ($nested > 0) {
                $expected = $nested_at >= $length
                    ? 'TC_VARNAME'
                    : "TC_FALLBACK or '}'";
                $error_line = $state['error_line'];
            } else {
                $expected = "'}'";
                $error_line = $state['error_line'];
            }
            $ok = ini_error_impl(
                $state,
                'end of file',
                $expected,
                $error_line
            );
            return '';
        }
        $fallback_text = \substr(
            $text,
            $fallback_start,
            $index - $fallback_start
        );
        if ($fallback_text === '') {
            $fallback = '';
        } else {
            $fallback_index = 0;
            [$fallback] = ini_concat_impl(
                $fallback_text,
                $fallback_index,
                $scanner_mode,
                true,
                $state,
                $ok,
                true
            );
            if (!$ok ||
                $fallback_index !== \strlen($fallback_text)) {
                return '';
            }
        }
    }

    $index++;
    $environment = \getenv($name);
    return $environment !== false
        ? $environment
        : ($fallback ?? '');
}

function ini_reserved_token_impl(string $value): ?string
{
    return match ($value) {
        'true', 'on', 'yes' => 'BOOL_TRUE',
        'false', 'off', 'no', 'none' => 'BOOL_FALSE',
        'null' => 'NULL_NULL',
        default => null,
    };
}

function ini_number_length_impl(string $text, int $index): int
{
    $length = \strlen($text);
    $start = $index;
    $negative = false;

    if ($text[$index] === '-' &&
        $index + 1 < $length &&
        ini_digit_impl($text[$index + 1])) {
        $negative = true;
        $index++;
    }
    if ($index < $length && ini_digit_impl($text[$index])) {
        while ($index < $length && ini_digit_impl($text[$index])) {
            $index++;
        }
        if (!$negative && $index < $length &&
            $text[$index] === '.') {
            $index++;
            while ($index < $length &&
                   ini_digit_impl($text[$index])) {
                $index++;
            }
        }
        return $index - $start;
    }
    if ($index === $start && $text[$index] === '.' &&
        $index + 1 < $length &&
        ini_digit_impl($text[$index + 1])) {
        $index += 2;
        while ($index < $length && ini_digit_impl($text[$index])) {
            $index++;
        }
        return $index - $start;
    }
    return 0;
}

function ini_typed_number_impl(string $value): int|float|string
{
    if (\strpos($value, '.') !== false) {
        return (float) $value;
    }

    $negative = $value !== '' && $value[0] === '-';
    $digits = $negative ? \substr($value, 1) : $value;
    $digits = \ltrim($digits, '0');
    if ($digits === '') {
        return 0;
    }

    $limit = $negative
        ? '9223372036854775808'
        : (string) PHP_INT_MAX;
    if (\strlen($digits) > \strlen($limit) ||
        (\strlen($digits) === \strlen($limit) &&
         \strcmp($digits, $limit) > 0)) {
        return $value;
    }
    if ($negative && $digits === '9223372036854775808') {
        return PHP_INT_MIN;
    }
    return (int) $value;
}

function ini_int32_impl(int $value): int
{
    $value &= 4294967295;
    return $value >= 2147483648
        ? $value - 4294967296
        : $value;
}

function ini_digit_impl(string $character): bool
{
    $byte = \ord($character);
    return $byte >= 48 && $byte <= 57;
}

function ini_constant_start_impl(string $character): bool
{
    $byte = \ord($character);
    return ($byte >= 65 && $byte <= 90) ||
        ($byte >= 97 && $byte <= 122) ||
        $character === '_';
}

function ini_constant_continue_impl(string $character): bool
{
    return ini_constant_start_impl($character) ||
        ini_digit_impl($character);
}

function ini_trim_space_impl(string $value): string
{
    return ini_rtrim_space_impl(ini_ltrim_space_impl($value));
}

function ini_ltrim_space_impl(string $value): string
{
    $index = 0;
    $length = \strlen($value);
    while ($index < $length &&
           ($value[$index] === ' ' ||
            $value[$index] === "\t")) {
        $index++;
    }
    return $index === 0 ? $value : \substr($value, $index);
}

function ini_rtrim_space_impl(string $value): string
{
    $length = \strlen($value);
    while ($length > 0 &&
           ($value[$length - 1] === ' ' ||
            $value[$length - 1] === "\t")) {
        $length--;
    }
    return $length === \strlen($value)
        ? $value
        : \substr($value, 0, $length);
}

function ini_newline_count_impl(string $value): int
{
    $count = 0;
    for ($index = 0, $length = \strlen($value);
         $index < $length;
         $index++) {
        if ($value[$index] === "\n") {
            $count++;
        } elseif ($value[$index] === "\r") {
            $count++;
            if ($index + 1 < $length &&
                $value[$index + 1] === "\n") {
                $index++;
            }
        }
    }
    return $count;
}

function ini_error_impl(
    array &$state,
    string $unexpected,
    ?string $expected,
    int $line
): false {
    $state['unexpected'] = $unexpected;
    $state['expected'] = $expected;
    $state['error_line'] = $line;
    return false;
}

function ini_syntax_warning_impl(array $state): void
{
    $message = 'syntax error, unexpected ' . $state['unexpected'];
    if ($state['expected'] !== null) {
        $message .= ', expecting ' . $state['expected'];
    }
    $message .= ' in ' . $state['filename'] .
        ' on line ' . $state['error_line'] . "\n";
    \PHP2Go\Internal\warning($message);
}


// pack and unpack deliberately share this PHP grammar. The only runtime help
// they need is the pair of IEEE-754 bit casts used for f/d directives.
function pack_impl(string $format, mixed ...$values): string
{
    $output = '';
    $position = 0;
    $argument = 0;
    $format_length = \strlen($format);

    for ($index = 0; $index < $format_length;) {
        $type = $format[$index++];
        $repeat = 1;
        $star = false;
        if ($index < $format_length && $format[$index] === '*') {
            $star = true;
            $repeat = -1;
            $index++;
        } elseif ($index < $format_length &&
                  pack_digit_impl($format[$index])) {
            $repeat = 0;
            while ($index < $format_length &&
                   pack_digit_impl($format[$index])) {
                $repeat = $repeat * 10 + \ord($format[$index]) - 48;
                $index++;
            }
        }

        if (\strpos('xX@', $type) !== false) {
            if ($star) {
                \PHP2Go\Internal\warning(
                    "pack(): Type {$type}: '*' ignored"
                );
                $repeat = 1;
            }
            if ($type === 'x') {
                pack_write_impl(
                    $output,
                    $position,
                    \str_repeat("\0", $repeat)
                );
            } elseif ($type === 'X') {
                if ($repeat > $position) {
                    \PHP2Go\Internal\warning(
                        'pack(): Type X: outside of string'
                    );
                    $position = 0;
                } else {
                    $position -= $repeat;
                }
            } else {
                pack_seek_impl($output, $position, $repeat);
            }
            continue;
        }

        if (\strpos('aAZhH', $type) !== false) {
            if ($argument >= \count($values)) {
                throw new \ValueError(
                    "Type {$type}: not enough arguments"
                );
            }
            $value = (string) $values[$argument++];
            if ($star) {
                $repeat = \strlen($value);
                if ($type === 'Z') {
                    $repeat++;
                }
            }
            if ($type === 'a' || $type === 'A' || $type === 'Z') {
                $pad = $type === 'A' ? ' ' : "\0";
                if ($type === 'Z') {
                    $bytes = $repeat === 0
                        ? ''
                        : \substr($value, 0, $repeat - 1) . "\0";
                } else {
                    $bytes = \substr($value, 0, $repeat);
                }
                if (\strlen($bytes) < $repeat) {
                    $bytes .= \str_repeat(
                        $pad,
                        $repeat - \strlen($bytes)
                    );
                }
                pack_write_impl($output, $position, $bytes);
                continue;
            }

            if (\strlen($value) < $repeat) {
                \PHP2Go\Internal\warning(
                    "pack(): Type {$type}: not enough characters in string"
                );
                $repeat = \strlen($value);
            }
            $bytes = '';
            for ($nibble = 0; $nibble < $repeat; $nibble += 2) {
                $first = pack_hex_nibble_impl(
                    $type,
                    $nibble < \strlen($value) ? $value[$nibble] : '0'
                );
                $second = pack_hex_nibble_impl(
                    $type,
                    $nibble + 1 < \strlen($value)
                        ? $value[$nibble + 1]
                        : '0'
                );
                $byte = $type === 'H'
                    ? ($first << 4) | $second
                    : $first | ($second << 4);
                $bytes .= \chr($byte);
            }
            pack_write_impl($output, $position, $bytes);
            continue;
        }

        $spec = pack_numeric_spec_impl($type);
        if ($spec === null) {
            throw new \ValueError(
                "Type {$type}: unknown format code"
            );
        }
        if ($star) {
            $repeat = \count($values) - $argument;
        }
        if ($argument + $repeat > \count($values)) {
            throw new \ValueError(
                "Type {$type}: too few arguments"
            );
        }
        for ($item = 0; $item < $repeat; $item++) {
            $value = $values[$argument++];
            if ($spec[3]) {
                $bytes = \PHP2Go\Internal\float_bytes(
                    (float) $value,
                    $spec[0],
                    $spec[1]
                );
            } else {
                $bytes = pack_integer_bytes_impl(
                    (int) $value,
                    $spec[0],
                    $spec[1]
                );
            }
            pack_write_impl($output, $position, $bytes);
        }
    }

    $unused = \count($values) - $argument;
    if ($unused > 0) {
        \PHP2Go\Internal\warning(
            "pack(): {$unused} arguments unused"
        );
    }
    return \substr($output, 0, $position);
}

function unpack_impl(
    string $format,
    string $data,
    int $offset = 0
): array|false {
    $data_length = \strlen($data);
    if ($offset < 0 || $offset > $data_length) {
        throw new \ValueError(
            'unpack(): Argument #3 ($offset) must be contained in ' .
            'argument #2 ($data)'
        );
    }

    $input = \substr($data, $offset);
    $input_length = \strlen($input);
    $position = 0;
    $result = [];
    $format_length = \strlen($format);

    for ($index = 0; $index < $format_length;) {
        $type = $format[$index++];
        if ($type === '/') {
            throw new \ValueError('Invalid format type /');
        }

        $repeat = 1;
        $star = false;
        if ($index < $format_length && $format[$index] === '*') {
            $star = true;
            $repeat = -1;
            $index++;
        } elseif ($index < $format_length &&
                  pack_digit_impl($format[$index])) {
            $digits = '';
            while ($index < $format_length &&
                   pack_digit_impl($format[$index])) {
                $digits .= $format[$index++];
            }
            $normalized = \ltrim($digits, '0');
            if ($normalized === '') {
                $normalized = '0';
            }
            if (\strlen($normalized) > 10 ||
                (\strlen($normalized) === 10 &&
                 $normalized > '2147483647')) {
                \PHP2Go\Internal\warning(
                    "unpack(): Type {$type}: integer overflow"
                );
                return false;
            }
            $repeat = (int) $normalized;
        }

        $name_start = $index;
        while ($index < $format_length && $format[$index] !== '/') {
            $index++;
        }
        $name = \substr($format, $name_start, $index - $name_start);
        if (\strlen($name) > 200) {
            $name = \substr($name, 0, 200);
        }
        if ($index < $format_length) {
            $index++;
        }

        if ($type === 'X') {
            if ($star) {
                \PHP2Go\Internal\warning(
                    "unpack(): Type X: '*' ignored"
                );
                $repeat = 1;
            }
            $position = $repeat > $position
                ? 0
                : $position - $repeat;
            continue;
        }
        if ($type === '@') {
            if ($star) {
                \PHP2Go\Internal\warning(
                    'unpack(): Type @: outside of string'
                );
                continue;
            }
            if ($repeat > $input_length) {
                \PHP2Go\Internal\warning(
                    'unpack(): Type @: outside of string'
                );
            } else {
                $position = $repeat;
            }
            continue;
        }
        if ($type === 'x') {
            if ($star) {
                $position = $input_length;
                continue;
            }
            if ($position + $repeat > $input_length) {
                unpack_short_warning_impl($type, 1, 0);
                return false;
            }
            $position += $repeat;
            continue;
        }

        if (\strpos('aAZhH', $type) !== false) {
            if ($type === 'h' || $type === 'H') {
                $nibbles = $star
                    ? ($input_length - $position) * 2
                    : $repeat;
                $bytes_needed = intdiv($nibbles + 1, 2);
            } else {
                $bytes_needed = $star
                    ? $input_length - $position
                    : $repeat;
            }
            if ($position + $bytes_needed > $input_length) {
                unpack_short_warning_impl(
                    $type,
                    $bytes_needed,
                    $input_length - $position
                );
                return false;
            }
            $bytes = \substr($input, $position, $bytes_needed);
            $position += $bytes_needed;

            if ($type === 'a') {
                $value = $bytes;
            } elseif ($type === 'A') {
                $value = \rtrim($bytes, "\0 \t\r\n");
            } elseif ($type === 'Z') {
                $zero = \strpos($bytes, "\0");
                $value = $zero === false
                    ? $bytes
                    : \substr($bytes, 0, $zero);
            } else {
                $value = '';
                for ($byte_index = 0;
                     $byte_index < $bytes_needed;
                     $byte_index++) {
                    $byte = \ord($bytes[$byte_index]);
                    if ($type === 'H') {
                        $value .= pack_hex_character_impl($byte >> 4);
                        $value .= pack_hex_character_impl($byte & 15);
                    } else {
                        $value .= pack_hex_character_impl($byte & 15);
                        $value .= pack_hex_character_impl($byte >> 4);
                    }
                }
                $value = \substr($value, 0, $nibbles);
            }
            unpack_store_impl($result, $name, 1, 1, $value);
            continue;
        }

        $spec = pack_numeric_spec_impl($type);
        if ($spec === null) {
            throw new \ValueError("Invalid format type {$type}");
        }
        $repetitions = $star
            ? intdiv($input_length - $position, $spec[0])
            : $repeat;
        for ($item = 1; $item <= $repetitions; $item++) {
            if ($position + $spec[0] > $input_length) {
                unpack_short_warning_impl(
                    $type,
                    $spec[0],
                    $input_length - $position
                );
                return false;
            }
            $bytes = \substr($input, $position, $spec[0]);
            $position += $spec[0];
            if ($spec[3]) {
                $value = \PHP2Go\Internal\bytes_float(
                    $bytes,
                    $spec[1]
                );
            } else {
                $value = unpack_integer_bytes_impl(
                    $bytes,
                    $spec[1],
                    $spec[2]
                );
            }
            unpack_store_impl(
                $result,
                $name,
                $star ? -1 : $repetitions,
                $item,
                $value
            );
        }
    }

    return $result;
}

function pack_numeric_spec_impl(string $type): ?array
{
    return match ($type) {
        'c' => [1, 1, true, false],
        'C' => [1, 1, false, false],
        's' => [2, 0, true, false],
        'S' => [2, 0, false, false],
        'n' => [2, 2, false, false],
        'v' => [2, 1, false, false],
        'i', 'l' => [4, 0, true, false],
        'I', 'L' => [4, 0, false, false],
        'N' => [4, 2, false, false],
        'V' => [4, 1, false, false],
        'q' => [8, 0, true, false],
        'Q' => [8, 0, false, false],
        'J' => [8, 2, false, false],
        'P' => [8, 1, false, false],
        'f' => [4, 0, false, true],
        'g' => [4, 1, false, true],
        'G' => [4, 2, false, true],
        'd' => [8, 0, false, true],
        'e' => [8, 1, false, true],
        'E' => [8, 2, false, true],
        default => null,
    };
}

function pack_integer_bytes_impl(
    int $value,
    int $size,
    int $order
): string {
    $little = $order === 1 ||
        ($order === 0 && pack_native_little_impl());
    $bytes = '';
    for ($index = 0; $index < $size; $index++) {
        $byte = \chr($value & 255);
        $bytes = $little ? $bytes . $byte : $byte . $bytes;
        $value >>= 8;
    }
    return $bytes;
}

function unpack_integer_bytes_impl(
    string $bytes,
    int $order,
    bool $signed
): int {
    $size = \strlen($bytes);
    $little = $order === 1 ||
        ($order === 0 && pack_native_little_impl());
    $value = 0;
    if ($little) {
        for ($index = $size - 1; $index >= 0; $index--) {
            $value = ($value << 8) | \ord($bytes[$index]);
        }
    } else {
        for ($index = 0; $index < $size; $index++) {
            $value = ($value << 8) | \ord($bytes[$index]);
        }
    }

    if ($signed && $size < 8) {
        $sign = 1 << ($size * 8 - 1);
        if (($value & $sign) !== 0) {
            $value -= 1 << ($size * 8);
        }
    }
    return $value;
}

function pack_native_little_impl(): bool
{
    return \PHP2Go\Internal\float_bytes(1.0, 4, 0) ===
        \PHP2Go\Internal\float_bytes(1.0, 4, 1);
}

function pack_write_impl(
    string &$output,
    int &$position,
    string $bytes
): void {
    $length = \strlen($bytes);
    for ($index = 0; $index < $length; $index++) {
        if ($position < \strlen($output)) {
            $output[$position] = $bytes[$index];
        } else {
            $output .= $bytes[$index];
        }
        $position++;
    }
}

function pack_seek_impl(
    string &$output,
    int &$position,
    int $target
): void {
    if ($target > \strlen($output)) {
        $output .= \str_repeat(
            "\0",
            $target - \strlen($output)
        );
    }
    $position = $target;
}

function pack_hex_nibble_impl(string $type, string $character): int
{
    $code = \ord($character);
    if ($code >= 48 && $code <= 57) {
        return $code - 48;
    }
    if ($code >= 65 && $code <= 70) {
        return $code - 55;
    }
    if ($code >= 97 && $code <= 102) {
        return $code - 87;
    }
    \PHP2Go\Internal\warning(
        "pack(): Type {$type}: illegal hex digit {$character}"
    );
    return 0;
}

function pack_hex_character_impl(int $value): string
{
    return \chr($value < 10 ? 48 + $value : 87 + $value);
}

function pack_digit_impl(string $character): bool
{
    return $character >= '0' && $character <= '9';
}

function unpack_store_impl(
    array &$result,
    string $name,
    int $repetitions,
    int $item,
    mixed $value
): void {
    $key = $name;
    if ($repetitions !== 1 || $name === '') {
        $key .= (string) $item;
    }
    $result[$key] = $value;
}

function unpack_short_warning_impl(
    string $type,
    int $need,
    int $available
): void {
    $verb = $available === 1 ? 'was' : 'were';
    \PHP2Go\Internal\warning(
        "unpack(): Type {$type}: not enough input values, " .
        "need {$need} values but only {$available} {$verb} provided"
    );
}




function sscanf_impl(
    string $string,
    string $format,
    mixed &...$vars
): array|int|null {
    return scanf_impl($string, $format, $vars);
}

/**
 * Shared scanner for sscanf() and fscanf(). The variadic array contains
 * references when output variables were supplied, so assigning one of its
 * elements writes through to the public caller.
 */
function scanf_impl(
    string $string,
    string $format,
    array &$vars
): array|int|null {
    $string = scanf_before_nul_impl($string);
    [$tokens, $total] = scanf_parse_format_impl(
        scanf_before_nul_impl($format),
        \count($vars)
    );

    $with_vars = \count($vars) > 0;
    $result = $with_vars ? null : \array_fill(0, $total, null);
    $position = 0;
    $length = \strlen($string);
    $conversions = 0;
    $underflow = false;

    foreach ($tokens as $token) {
        if ($token[0] === 0) {
            if ($position >= $length) {
                $underflow = true;
                break;
            }
            $character = $string[$position++];
            if ($character !== $token[1]) {
                break;
            }
            continue;
        }

        if ($token[0] === 1) {
            while ($position < $length &&
                   scanf_space_impl($string[$position])) {
                $position++;
            }
            continue;
        }

        $type = $token[1];
        $suppress = $token[2];
        $assignment = $token[3];
        $width = $token[4];

        if ($type === 'n') {
            if (!$suppress) {
                if ($with_vars) {
                    $vars[$assignment] = $position;
                } else {
                    $result[$assignment] = $position;
                }
            }
            $conversions++;
            continue;
        }

        if ($position >= $length) {
            $underflow = true;
            break;
        }

        if ($type !== 'c' && $type !== '[') {
            while ($position < $length &&
                   scanf_space_impl($string[$position])) {
                $position++;
            }
            if ($position >= $length) {
                $underflow = true;
                break;
            }
        }

        $matched = true;
        $field_underflow = false;
        $value = null;
        if ($type === 's' || $type === 'c') {
            if ($type === 'c' && $width === 0) {
                $width = 1;
            }
            $limit = $width === 0
                ? $length - $position
                : $width;
            $end = $position;
            while ($end < $length &&
                   !scanf_space_impl($string[$end]) &&
                   $end - $position < $limit) {
                $end++;
            }
            $value = \substr($string, $position, $end - $position);
            $position = $end;
        } elseif ($type === '[') {
            $limit = $width === 0
                ? $length - $position
                : $width;
            $end = $position;
            while ($end < $length &&
                   $end - $position < $limit &&
                   scanf_charset_contains_impl($token[5], $string[$end])) {
                $end++;
            }
            if ($end === $position) {
                $matched = false;
            } else {
                $value = \substr(
                    $string,
                    $position,
                    $end - $position
                );
                $position = $end;
            }
        } elseif ($type === 'f' || $type === 'e' ||
                  $type === 'E' || $type === 'g') {
            [$matched, $value, $field_underflow] =
                scanf_float_impl($string, $position, $width);
        } else {
            [$matched, $value, $field_underflow] =
                scanf_integer_impl($string, $position, $width, $type);
        }

        if (!$matched) {
            $underflow = $field_underflow;
            break;
        }
        if (!$suppress) {
            if ($with_vars) {
                $vars[$assignment] = $value;
            } else {
                $result[$assignment] = $value;
            }
        }
        $conversions++;
    }

    if ($underflow && $conversions === 0) {
        return $with_vars ? -1 : null;
    }
    return $with_vars ? $conversions : $result;
}

/**
 * Format tokens: [0, byte] literal, [1] whitespace, or
 * [2, type, suppress, assignment, width, charset].
 */
function scanf_parse_format_impl(
    string $format,
    int $num_vars
): array {
    $tokens = [];
    $assignments = [];
    $length = \strlen($format);
    $index = 0;
    $sequential = 0;
    $got_xpg = false;
    $got_sequential = false;
    $xpg_size = 0;

    while ($index < $length) {
        $character = $format[$index++];
        if (scanf_space_impl($character)) {
            $tokens[] = [1];
            continue;
        }
        if ($character !== '%') {
            $tokens[] = [0, $character];
            continue;
        }

        $type = $index < $length ? $format[$index++] : "\0";
        if ($type === '%') {
            $tokens[] = [0, '%'];
            continue;
        }

        $suppress = false;
        if ($type === '*') {
            $suppress = true;
            $type = $index < $length ? $format[$index++] : "\0";
        }

        $assignment = null;
        $width = 0;
        if (scanf_digit_impl($type)) {
            $digits = $type;
            while ($index < $length &&
                   scanf_digit_impl($format[$index])) {
                $digits .= $format[$index++];
            }
            if (!$suppress &&
                $index < $length &&
                $format[$index] === '$') {
                $index++;
                $got_xpg = true;
                if ($got_sequential) {
                    throw new \ValueError(
                        'cannot mix "%" and "%n$" conversion specifiers'
                    );
                }
                if (scanf_decimal_exceeds_impl(
                    $digits,
                    $num_vars > 0 ? $num_vars : 255
                )) {
                    throw new \ValueError(
                        '"%n$" argument index out of range'
                    );
                }
                $position = (int) $digits;
                if ($position < 1) {
                    throw new \ValueError(
                        '"%n$" argument index out of range'
                    );
                }
                $assignment = $position - 1;
                if ($num_vars > 0 && $assignment >= $num_vars) {
                    throw new \ValueError(
                        '"%n$" argument index out of range'
                    );
                }
                if ($position > $xpg_size) {
                    $xpg_size = $position;
                }
                $type = $index < $length
                    ? $format[$index++]
                    : "\0";
            } else {
                $width = scanf_decimal_width_impl($digits);
                $type = $index < $length
                    ? $format[$index++]
                    : "\0";
            }
        }

        if ($type === 'l' || $type === 'L' || $type === 'h') {
            $type = $index < $length
                ? $format[$index++]
                : "\0";
        }

        $charset = null;
        if ($type === '[') {
            [$charset, $index] =
                scanf_parse_charset_impl($format, $index);
        } elseif (\strpos('ndDioxXufFeEgsc', $type) === false) {
            if ($type === "\0") {
                throw new \ValueError(
                    'Bad scan conversion character "'
                );
            }
            throw new \ValueError(
                'Bad scan conversion character "' . $type . '"'
            );
        }

        if (!$suppress) {
            if ($assignment === null) {
                $got_sequential = true;
                if ($got_xpg) {
                    throw new \ValueError(
                        'cannot mix "%" and "%n$" conversion specifiers'
                    );
                }
                $assignment = $sequential++;
                if ($num_vars > 0 && $assignment >= $num_vars) {
                    throw new \ValueError(
                        'Different numbers of variable names and field specifiers'
                    );
                }
            }
            $assignments[$assignment] =
                ($assignments[$assignment] ?? 0) + 1;
        } else {
            $assignment = -1;
        }

        $tokens[] = [
            2,
            $type,
            $suppress,
            $assignment,
            $width,
            $charset,
        ];
    }

    $total = $got_xpg ? $xpg_size : $sequential;
    if ($num_vars > 0) {
        $total = $num_vars;
    }
    for ($position = 0; $position < $total; $position++) {
        $assigned = $assignments[$position] ?? 0;
        if ($assigned > 1) {
            throw new \ValueError(
                'Variable is assigned by multiple "%n$" conversion specifiers'
            );
        }
        if ($num_vars > 0 && $assigned === 0) {
            throw new \ValueError(
                'Variable is not assigned by any conversion specifiers'
            );
        }
    }

    return [$tokens, $total];
}

function scanf_parse_charset_impl(
    string $format,
    int $index
): array {
    $length = \strlen($format);
    if ($index >= $length) {
        throw new \ValueError('Unmatched [ in format string');
    }

    $exclude = false;
    if ($format[$index] === '^') {
        $exclude = true;
        $index++;
        if ($index >= $length) {
            throw new \ValueError('Unmatched [ in format string');
        }
    }

    $body = '';
    if ($format[$index] === ']') {
        $body .= ']';
        $index++;
        if ($index >= $length) {
            throw new \ValueError('Unmatched [ in format string');
        }
    }
    while ($index < $length && $format[$index] !== ']') {
        $body .= $format[$index++];
    }
    if ($index >= $length) {
        throw new \ValueError('Unmatched [ in format string');
    }

    return [
        scanf_build_charset_impl($body, $exclude),
        $index + 1,
    ];
}

function scanf_build_charset_impl(
    string $body,
    bool $exclude
): array {
    $characters = '';
    $ranges = [];
    $length = \strlen($body);
    $index = 0;

    if ($index < $length && $body[$index] === '-') {
        $characters .= '-';
        $index++;
    }
    while ($index < $length) {
        if ($index + 2 < $length &&
            $body[$index + 1] === '-') {
            $start = \ord($body[$index]);
            $end = \ord($body[$index + 2]);
            $ranges[] = $start <= $end
                ? [$start, $end]
                : [$end, $start];
            $index += 3;
            continue;
        }
        $characters .= $body[$index++];
    }

    return [$exclude, $characters, $ranges];
}

function scanf_charset_contains_impl(
    array $charset,
    string $character
): bool {
    $match = \strpos($charset[1], $character) !== false;
    if (!$match) {
        $byte = \ord($character);
        foreach ($charset[2] as $range) {
            if ($byte >= $range[0] && $byte <= $range[1]) {
                $match = true;
                break;
            }
        }
    }
    return $charset[0] ? !$match : $match;
}

function scanf_integer_impl(
    string $string,
    int &$position,
    int $width,
    string $type
): array {
    $length = \strlen($string);
    $limit = $width === 0 || $width > 63 ? 63 : $width;
    $start = $position;
    $remaining = $limit;
    $signed_prefix = false;

    if ($remaining > 0 && $position < $length &&
        ($string[$position] === '+' ||
         $string[$position] === '-')) {
        $signed_prefix = true;
        $position++;
        $remaining--;
    }

    $base = match ($type) {
        'o' => 8,
        'x', 'X' => 16,
        'i' => 0,
        default => 10,
    };
    $digits = 0;

    if ($remaining > 0 && $position < $length &&
        $string[$position] === '0') {
        $position++;
        $remaining--;
        $digits = 1;
        if ($base === 0) {
            $base = 8;
        }

        if (($type === 'i' || $type === 'x' || $type === 'X') &&
            !$signed_prefix &&
            $remaining > 0 &&
            $position < $length &&
            ($string[$position] === 'x' ||
             $string[$position] === 'X')) {
            $position++;
            $remaining--;
            $base = 16;
            if ($remaining === 0 ||
                $position >= $length ||
                !scanf_base_digit_impl($string[$position], 16)) {
                $position--;
                $remaining++;
            } else {
                while ($remaining > 0 &&
                       $position < $length &&
                       scanf_base_digit_impl(
                           $string[$position],
                           16
                       )) {
                    $position++;
                    $remaining--;
                    $digits++;
                }
            }
        }
    } else {
        if ($base === 0) {
            $base = 10;
        }
        while ($remaining > 0 &&
               $position < $length &&
               scanf_base_digit_impl(
                   $string[$position],
                   $base
               )) {
            $position++;
            $remaining--;
            $digits++;
        }
    }

    while ($remaining > 0 &&
           $position < $length &&
           scanf_base_digit_impl(
               $string[$position],
               $base
           )) {
        $position++;
        $remaining--;
        $digits++;
    }

    if ($digits === 0) {
        return [
            false,
            null,
            $position >= $length,
        ];
    }

    $token = \substr($string, $start, $position - $start);
    return [
        true,
        scanf_integer_value_impl($token, $base, $type === 'u'),
        false,
    ];
}

function scanf_integer_value_impl(
    string $token,
    int $base,
    bool $unsigned
): int|string {
    $negative = $token !== '' && $token[0] === '-';
    $start = ($negative || ($token !== '' && $token[0] === '+'))
        ? 1
        : 0;
    if ($base === 16 &&
        $start + 1 < \strlen($token) &&
        $token[$start] === '0' &&
        ($token[$start + 1] === 'x' ||
         $token[$start + 1] === 'X')) {
        $start += 2;
    }

    [$high, $low, $_end, $overflow] =
        ini_quantity_parse_unsigned_impl(
            $token,
            $start,
            \strlen($token),
            $base
        );

    if ($unsigned) {
        if ($overflow) {
            $high = 4294967295;
            $low = 4294967295;
        } elseif ($negative) {
            [$high, $low] =
                ini_quantity_negate_impl($high, $low);
        }
        if ($high < 2147483648) {
            return $high * 4294967296 + $low;
        }
        return scanf_unsigned_decimal_impl($high, $low);
    }

    if ($negative) {
        if ($overflow ||
            $high > 2147483648 ||
            ($high === 2147483648 && $low > 0)) {
            return PHP_INT_MIN;
        }
        if ($high === 2147483648 && $low === 0) {
            return PHP_INT_MIN;
        }
        return -($high * 4294967296 + $low);
    }
    if ($overflow || $high >= 2147483648) {
        return PHP_INT_MAX;
    }
    return $high * 4294967296 + $low;
}

function scanf_unsigned_decimal_impl(
    int $high,
    int $low
): string {
    $digits = '';
    while ($high !== 0 || $low !== 0) {
        $high_quotient = \intdiv($high, 10);
        $combined = ($high % 10) * 4294967296 + $low;
        $low = \intdiv($combined, 10);
        $digits = \chr(48 + ($combined % 10)) . $digits;
        $high = $high_quotient;
    }
    return $digits === '' ? '0' : $digits;
}

function scanf_float_impl(
    string $string,
    int &$position,
    int $width
): array {
    $length = \strlen($string);
    $limit = $width === 0 || $width > 63 ? 63 : $width;
    $start = $position;
    $end_limit = $position + $limit;
    if ($end_limit > $length) {
        $end_limit = $length;
    }

    if ($position < $end_limit &&
        ($string[$position] === '+' ||
         $string[$position] === '-')) {
        $position++;
    }

    $mantissa_digits = 0;
    $point = false;
    while ($position < $end_limit) {
        $character = $string[$position];
        if (scanf_digit_impl($character)) {
            $mantissa_digits++;
            $position++;
            continue;
        }
        if ($character === '.' && !$point) {
            $point = true;
            $position++;
            continue;
        }
        break;
    }

    if ($mantissa_digits === 0) {
        return [
            false,
            null,
            $position >= $length,
        ];
    }

    $exponent = $position;
    if ($position < $end_limit &&
        ($string[$position] === 'e' ||
         $string[$position] === 'E')) {
        $position++;
        if ($position < $end_limit &&
            ($string[$position] === '+' ||
             $string[$position] === '-')) {
            $position++;
        }
        $digits_start = $position;
        while ($position < $end_limit &&
               scanf_digit_impl($string[$position])) {
            $position++;
        }
        if ($position === $digits_start) {
            $position = $exponent;
        }
    }

    $token = \substr($string, $start, $position - $start);
    return [true, (float) $token, false];
}

function scanf_before_nul_impl(string $value): string
{
    $nul = \strpos($value, "\0");
    return $nul === false ? $value : \substr($value, 0, $nul);
}

function scanf_space_impl(string $character): bool
{
    $byte = \ord($character);
    return $byte === 32 || ($byte >= 9 && $byte <= 13);
}

function scanf_digit_impl(string $character): bool
{
    $byte = \ord($character);
    return $byte >= 48 && $byte <= 57;
}

function scanf_base_digit_impl(
    string $character,
    int $base
): bool {
    $digit = ini_quantity_digit_impl($character);
    return $digit >= 0 && $digit < $base;
}

function scanf_decimal_exceeds_impl(
    string $digits,
    int $limit
): bool {
    $digits = \ltrim($digits, '0');
    if ($digits === '') {
        return false;
    }
    $maximum = (string) $limit;
    return \strlen($digits) > \strlen($maximum) ||
        (\strlen($digits) === \strlen($maximum) &&
         \strcmp($digits, $maximum) > 0);
}

function scanf_decimal_width_impl(string $digits): int
{
    return scanf_decimal_exceeds_impl($digits, PHP_INT_MAX)
        ? PHP_INT_MAX
        : (int) $digits;
}

function mail_before_nul_impl(string $value): string
{
    $nul = \strpos($value, "\0");
    return $nul === false ? $value : \substr($value, 0, $nul);
}

function mail_path_validate_impl(
    string $value,
    int $argument,
    string $name
): void {
    if (\strpos($value, "\0") !== false) {
        throw new \ValueError(
            'mail(): Argument #' . $argument . ' ($' . $name .
            ') must not contain any null bytes'
        );
    }
}

function mail_header_name_validate_impl(string $name): void
{
    for ($index = 0, $length = \strlen($name); $index < $length; ++$index) {
        $byte = \ord($name[$index]);
        if ($byte < 33 || $byte > 126 || $name[$index] === ':') {
            throw new \ValueError(
                'Header name "' . $name . '" contains invalid characters'
            );
        }
    }
}

function mail_header_value_validate_impl(string $name, string $value): void
{
    $length = \strlen($value);
    $index = 0;
    while ($index < $length) {
        $character = $value[$index];
        if ($character === "\r") {
            if ($index + 1 >= $length || $value[$index + 1] !== "\n") {
                throw new \ValueError(
                    'Header "' . $name . '" contains CR character that is ' .
                    'not allowed in the header'
                );
            }
            if ($index + 2 < $length &&
                ($value[$index + 2] === ' ' ||
                 $value[$index + 2] === "\t")) {
                $index += 3;
                continue;
            }
            throw new \ValueError(
                'Header "' . $name . '" contains CRLF characters that are ' .
                'used as a line separator and are not allowed in the header'
            );
        }
        if ($character === "\n") {
            if ($index + 1 < $length &&
                ($value[$index + 1] === ' ' ||
                 $value[$index + 1] === "\t")) {
                $index += 2;
                continue;
            }
            throw new \ValueError(
                'Header "' . $name . '" contains LF character that is not ' .
                'allowed in the header'
            );
        }
        if ($character === "\0") {
            throw new \ValueError(
                'Header "' . $name . '" contains NULL character that is not ' .
                'allowed in the header'
            );
        }
        ++$index;
    }
}

function mail_header_append_impl(array &$lines, string $name, mixed $value): void
{
    if (\is_string($value)) {
        mail_header_name_validate_impl($name);
        mail_header_value_validate_impl($name, $value);
        $lines[] = $name . ': ' . $value;
        return;
    }
    if (!\is_array($value)) {
        throw new \TypeError(
            'Header "' . $name . '" must be of type array|string, ' .
            \get_debug_type($value) . ' given'
        );
    }
    foreach ($value as $key => $element) {
        if (\is_string($key)) {
            throw new \TypeError(
                'Header "' . $name . '" must only contain numeric keys, "' .
                $key . '" found'
            );
        }
        if (!\is_string($element)) {
            throw new \TypeError(
                'Header "' . $name . '" must only contain values of type ' .
                'string, ' . \get_debug_type($element) . ' found'
            );
        }
        mail_header_name_validate_impl($name);
        mail_header_value_validate_impl($name, $element);
        $lines[] = $name . ': ' . $element;
    }
}

function mail_headers_impl(array|string $headers): string
{
    if (\is_string($headers)) {
        if (\strpos($headers, "\0") !== false) {
            throw new \ValueError(
                'mail(): Argument #4 ($additional_headers) must not contain ' .
                'any null bytes'
            );
        }
        return \rtrim($headers);
    }

    $lines = [];
    $single = [
        'orig-date' => true,
        'from' => true,
        'sender' => true,
        'reply-to' => true,
        'cc' => true,
        'bcc' => true,
        'message-id' => true,
        'references' => true,
        'in-reply-to' => true,
    ];
    foreach ($headers as $name => $value) {
        if (\is_int($name)) {
            throw new \TypeError(
                'Header name cannot be numeric, ' . $name . ' given'
            );
        }
        $lower = \strtolower($name);
        if ($lower === 'to') {
            throw new \ValueError(
                'The additional headers cannot contain the "To" header'
            );
        }
        if ($lower === 'subject') {
            throw new \ValueError(
                'The additional headers cannot contain the "Subject" header'
            );
        }
        if (isset($single[$lower]) && \is_array($value)) {
            throw new \TypeError(
                'Header "' . $lower . '" must be of type string, array given'
            );
        }
        mail_header_append_impl($lines, $name, $value);
    }
    return \implode("\r\n", $lines);
}

function mail_normalize_field_impl(string $value): string
{
    $length = \strlen($value);
    while ($length > 0) {
        $byte = \ord($value[$length - 1]);
        if (!($byte === 32 || ($byte >= 9 && $byte <= 13))) {
            break;
        }
        --$length;
    }
    $value = \substr($value, 0, $length);
    $result = '';
    for ($index = 0; $index < $length; ++$index) {
        $byte = \ord($value[$index]);
        if (($byte < 32 || $byte === 127) &&
            $value[$index] === "\r" &&
            $index + 2 < $length &&
            $value[$index + 1] === "\n" &&
            ($value[$index + 2] === ' ' ||
             $value[$index + 2] === "\t")) {
            $result .= "\r\n";
            ++$index;
            continue;
        }
        $result .= ($byte < 32 || $byte === 127) ? ' ' : $value[$index];
    }
    return $result;
}

function mail_malformed_headers_impl(string $headers): bool
{
    $length = \strlen($headers);
    if ($length === 0) {
        return false;
    }
    $first = \ord($headers[0]);
    if ($first < 33 || $first > 126 || $headers[0] === ':') {
        return true;
    }
    $index = 0;
    while ($index < $length) {
        if ($headers[$index] === "\r") {
            if ($index + 1 >= $length ||
                $headers[$index + 1] === "\r" ||
                ($headers[$index + 1] === "\n" &&
                 ($index + 2 >= $length ||
                  $headers[$index + 2] === "\n" ||
                  $headers[$index + 2] === "\r"))) {
                return true;
            }
            $index += 2;
            continue;
        }
        if ($headers[$index] === "\n") {
            if ($index + 1 >= $length ||
                $headers[$index + 1] === "\r" ||
                $headers[$index + 1] === "\n") {
                return true;
            }
            $index += 2;
            continue;
        }
        ++$index;
    }
    return false;
}

function mail_payload_impl(
    string $to,
    string $subject,
    string $message,
    string $headers
): string {
    $payload = 'To: ' . $to . "\r\nSubject: " . $subject . "\r\n";
    if ($headers !== '') {
        $payload .= $headers . "\r\n";
    }
    return $payload . "\r\n" . $message . "\r\n";
}

function mail_deliver_impl(
    string $function,
    string $payload,
    string $extra_parameters
): bool {
    [$started, $status, $sendmail_path, $error_number] =
        \PHP2Go\Internal\mail_send($payload, $extra_parameters);
    if ($sendmail_path === '') {
        return false;
    }
    if (!$started) {
        \PHP2Go\Internal\warning(
            $function . '(): Could not execute mail delivery program \'' .
            $sendmail_path . '\''
        );
        return false;
    }
    if ($error_number === 13) {
        \PHP2Go\Internal\warning(
            $function . '(): Permission denied: unable to execute shell to ' .
            'run mail delivery binary \'' . $sendmail_path . '\''
        );
        return false;
    }
    return $status === 0 || $status === 75;
}

function mail_impl(
    string $to,
    string $subject,
    string $message,
    array|string $additional_headers = [],
    string $additional_params = ''
): bool {
    mail_path_validate_impl($to, 1, 'to');
    mail_path_validate_impl($subject, 2, 'subject');
    mail_path_validate_impl($message, 3, 'message');
    mail_path_validate_impl($additional_params, 5, 'additional_params');
    $headers = mail_headers_impl($additional_headers);
    if (mail_malformed_headers_impl($headers)) {
        \PHP2Go\Internal\warning(
            'mail(): Multiple or malformed newlines found in additional_header'
        );
        return false;
    }
    $to = mail_normalize_field_impl($to);
    $subject = mail_normalize_field_impl($subject);
    $extra = $additional_params === ''
        ? ''
        : escapeshellcmd_impl($additional_params);
    return mail_deliver_impl(
        'mail',
        mail_payload_impl($to, $subject, $message, $headers),
        $extra
    );
}

function error_log_impl(
    string $message,
    int $message_type = 0,
    ?string $destination = null,
    ?string $additional_headers = null
): bool {
    if ($destination !== null && \strpos($destination, "\0") !== false) {
        throw new \ValueError(
            'error_log(): Argument #3 ($destination) must not contain any ' .
            'null bytes'
        );
    }
    $message_type = ini_int32_impl($message_type);
    if ($message_type === 1) {
        $headers = mail_before_nul_impl($additional_headers ?? '');
        if (mail_malformed_headers_impl($headers)) {
            \PHP2Go\Internal\warning(
                'error_log(): Multiple or malformed newlines found in ' .
                'additional_header'
            );
            return false;
        }
        return mail_deliver_impl(
            'error_log',
            mail_payload_impl(
                $destination ?? '',
                'PHP error_log message',
                mail_before_nul_impl($message),
                $headers
            ),
            ''
        );
    }
    if ($message_type === 2) {
        throw new \ValueError(
            'TCP/IP option is not available for error logging'
        );
    }
    if ($message_type === 3) {
        if ($destination === null || $destination === '') {
            throw new \ValueError('Path must not be empty');
        }
        [$success, $diagnostics] = \PHP2Go\Internal\append_file(
            $destination,
            $message
        );
        foreach ($diagnostics as [$level, $diagnostic]) {
            if ($level === 'notice') {
                \PHP2Go\Internal\notice($diagnostic);
            } else {
                \PHP2Go\Internal\warning($diagnostic);
            }
        }
        return $success;
    }
    \PHP2Go\Internal\sapi_log(mail_before_nul_impl($message));
    return true;
}
