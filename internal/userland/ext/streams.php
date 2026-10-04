<?php

namespace PHP2Go\Extension\Streams;

function stream_resolve_include_path_impl(mixed $filename): string|false
{
    $filename = (string) $filename;
    if (\strpos($filename, "\0") !== false) {
        throw new \ValueError(
            'stream_resolve_include_path(): Argument #1 ($filename) ' .
            'must not contain any null bytes'
        );
    }
    return \PHP2Go\Internal\resolve_include_path($filename);
}

function stream_get_wrappers_impl(): array
{
    return \PHP2Go\Internal\stream_wrapper_registry('list');
}

function stream_get_transports_impl(): array
{
    return ['tcp', 'udp', 'unix', 'udg'];
}

function stream_context_options_normalize_impl(array $options): array
{
    $normalized = [];
    foreach ($options as $wrapper => $wrapper_options) {
        if (!\is_string($wrapper) || !\is_array($wrapper_options)) {
            throw new \ValueError(
                'Options should have the form ' .
                '["wrappername"]["optionname"] = $value'
            );
        }
        $wrapper_normalized = [];
        foreach ($wrapper_options as $option => $value) {
            if (\is_string($option)) {
                $wrapper_normalized[$option] = $value;
            }
        }
        if ($wrapper_normalized !== []) {
            $normalized[$wrapper] = $wrapper_normalized;
        }
    }
    return $normalized;
}

function stream_context_params_normalize_impl(array $params): array
{
    $has_notification = \array_key_exists('notification', $params);
    $notification = $has_notification ? $params['notification'] : null;
    $options = [];
    if (\array_key_exists('options', $params)) {
        if (!\is_array($params['options'])) {
            throw new \TypeError('Invalid stream/context parameter');
        }
        $options = stream_context_options_normalize_impl($params['options']);
    }
    return [$has_notification, $notification, $options];
}

function stream_context_resource_validate_impl(
    mixed $value,
    string $function,
    string $parameter
): void {
    if (!\is_resource($value)) {
        if (\get_debug_type($value) === 'resource (closed)') {
            throw new \TypeError(
                $function . '(): Argument #1 ($' . $parameter .
                ') must be a valid stream/context'
            );
        }
        throw new \TypeError(
            $function . '(): Argument #1 ($' . $parameter .
            ') must be of type resource, ' . \get_debug_type($value) . ' given'
        );
    }
    $type = \get_resource_type($value);
    if ($type !== 'stream' && $type !== 'stream-context') {
        throw new \TypeError(
            $function . '(): Argument #1 ($' . $parameter .
            ') must be a valid stream/context'
        );
    }
}

function stream_context_params_apply_impl(
    mixed $context,
    array $params,
    string $function
): true {
    stream_context_resource_validate_impl($context, $function, 'context');
    [$has_notification, $notification, $options] =
        stream_context_params_normalize_impl($params);
    \PHP2Go\Internal\stream_context_merge_options($context, $options);
    if ($has_notification) {
        \PHP2Go\Internal\stream_context_set_notification($context, $notification);
    }
    return true;
}

function stream_context_default_resource_impl(): mixed
{
    static $context = null;
    if ($context === null) {
        $context = \PHP2Go\Internal\stream_context_create();
    }
    return $context;
}

function stream_context_create_impl(
    mixed $options = null,
    mixed $params = null
): mixed {
    $normalized_options = stream_context_options_normalize_impl($options ?? []);
    [$has_notification, $notification, $param_options] =
        stream_context_params_normalize_impl($params ?? []);
    $context = \PHP2Go\Internal\stream_context_create();
    \PHP2Go\Internal\stream_context_merge_options($context, $normalized_options);
    \PHP2Go\Internal\stream_context_merge_options($context, $param_options);
    if ($has_notification) {
        \PHP2Go\Internal\stream_context_set_notification($context, $notification);
    }
    return $context;
}

function stream_context_get_default_impl(mixed $options = null): mixed
{
    $context = stream_context_default_resource_impl();
    if ($options !== null) {
        \PHP2Go\Internal\stream_context_merge_options(
            $context,
            stream_context_options_normalize_impl($options)
        );
    }
    return $context;
}

function stream_context_set_default_impl(array $options): mixed
{
    $context = stream_context_default_resource_impl();
    \PHP2Go\Internal\stream_context_merge_options(
        $context,
        stream_context_options_normalize_impl($options)
    );
    return $context;
}

function stream_context_get_options_impl(mixed $stream_or_context): array
{
    stream_context_resource_validate_impl(
        $stream_or_context,
        'stream_context_get_options',
        'stream_or_context'
    );
    return \PHP2Go\Internal\stream_context_get_options($stream_or_context);
}

function stream_context_get_params_impl(mixed $context): array
{
    stream_context_resource_validate_impl(
        $context,
        'stream_context_get_params',
        'context'
    );
    return \PHP2Go\Internal\stream_context_get_params($context);
}

function stream_context_set_options_impl(
    mixed $context,
    array $options
): true {
    stream_context_resource_validate_impl(
        $context,
        'stream_context_set_options',
        'context'
    );
    \PHP2Go\Internal\stream_context_merge_options(
        $context,
        stream_context_options_normalize_impl($options)
    );
    return true;
}

function stream_context_set_params_impl(
    mixed $context,
    array $params
): true {
    return stream_context_params_apply_impl(
        $context,
        $params,
        'stream_context_set_params'
    );
}

function stream_context_set_option_impl(
    mixed $context,
    mixed $wrapper_or_options,
    mixed $option_name = null,
    mixed $value = null
): true {
    stream_context_resource_validate_impl(
        $context,
        'stream_context_set_option',
        'context'
    );
    if (\is_array($wrapper_or_options)) {
        if ($option_name !== null) {
            throw new \ValueError(
                'stream_context_set_option(): Argument #3 ($option_name) ' .
                'must be null when argument #2 ($wrapper_or_options) is an array'
            );
        }
        if (\func_num_args() >= 4) {
            throw new \ValueError(
                'stream_context_set_option(): Argument #4 ($value) cannot be ' .
                'provided when argument #2 ($wrapper_or_options) is an array'
            );
        }
        $options = stream_context_options_normalize_impl($wrapper_or_options);
    } else {
        if ($option_name === null) {
            throw new \ValueError(
                'stream_context_set_option(): Argument #3 ($option_name) cannot ' .
                'be null when argument #2 ($wrapper_or_options) is a string'
            );
        }
        if (\func_num_args() < 4) {
            throw new \ValueError(
                'stream_context_set_option(): Argument #4 ($value) must be ' .
                'provided when argument #2 ($wrapper_or_options) is a string'
            );
        }
        $options = [$wrapper_or_options => [$option_name => $value]];
    }
    \PHP2Go\Internal\stream_context_merge_options($context, $options);
    return true;
}

function stream_local_scheme_impl(string $path): ?string
{
    if (\substr($path, 0, 5) === 'data:') {
        return 'data';
    }
    $separator = \strpos($path, '://');
    if ($separator === false || $separator <= 1) {
        return null;
    }
    $scheme = \substr($path, 0, $separator);
    for ($index = 0; $index < \strlen($scheme); $index++) {
        $byte = \ord($scheme[$index]);
        if (
            ($byte < 48 || $byte > 57) &&
            ($byte < 65 || $byte > 90) &&
            ($byte < 97 || $byte > 122) &&
            $scheme[$index] !== '+' &&
            $scheme[$index] !== '-' &&
            $scheme[$index] !== '.'
        ) {
            return null;
        }
    }
    return $scheme;
}

function stream_local_path_impl(string $path): bool
{
    $scheme = stream_local_scheme_impl($path);
    if ($scheme === null) {
        return true;
    }
    $wrappers = [
        'php' => true,
        'file' => true,
        'glob' => true,
        'data' => false,
        'http' => false,
        'ftp' => false,
    ];
    $lookup = \strtolower($scheme);
    if (!\array_key_exists($lookup, $wrappers)) {
        \PHP2Go\Internal\warning(
            'stream_is_local(): Unable to find the wrapper "' . $scheme .
            '" - did you forget to enable it when you configured PHP?'
        );
        return true;
    }
    return $wrappers[$lookup];
}

function stream_is_local_impl(mixed $stream): bool
{
    if (
        \is_resource($stream) ||
        \get_debug_type($stream) === 'resource (closed)'
    ) {
        stream_format_validate_impl($stream, 'stream_is_local');
        $metadata = \stream_get_meta_data($stream);
        if (($metadata['stream_type'] ?? '') === 'generic_socket') {
            return false;
        }
        $wrapper = \strtolower((string) ($metadata['wrapper_type'] ?? ''));
        return $wrapper !== 'rfc2397' && $wrapper !== 'http' && $wrapper !== 'ftp';
    }
    if (\is_array($stream)) {
        \PHP2Go\Internal\warning('Array to string conversion');
        $path = 'Array';
    } else {
        $path = (string) $stream;
    }
    return stream_local_path_impl($path);
}

function stream_isatty_impl(mixed $stream): bool
{
    stream_format_validate_impl($stream, 'stream_isatty');
    return \PHP2Go\Internal\stream_isatty($stream);
}

function stream_set_blocking_impl($stream, bool $enable): bool
{
    return \PHP2Go\Internal\stream_set_blocking($stream, $enable);
}

function stream_set_timeout_impl(
    $stream,
    int $seconds,
    int $microseconds = 0
): bool {
    return \PHP2Go\Internal\stream_set_timeout($stream, $seconds, $microseconds);
}

function stream_set_read_buffer_impl($stream, int $size): int
{
    return \PHP2Go\Internal\stream_set_read_buffer($stream, $size);
}

function stream_set_write_buffer_impl($stream, int $size): int
{
    return \PHP2Go\Internal\stream_set_write_buffer($stream, $size);
}

function stream_set_chunk_size_impl($stream, int $size): int
{
    if ($size <= 0) {
        throw new \ValueError(
            'stream_set_chunk_size(): Argument #2 ($size) must be greater than 0'
        );
    }
    if ($size > 2147483647) {
        throw new \ValueError(
            'stream_set_chunk_size(): Argument #2 ($size) is too large'
        );
    }
    return \PHP2Go\Internal\stream_set_chunk_size($stream, $size);
}

function socket_get_status_impl($stream): array
{
    return \PHP2Go\Internal\stream_metadata($stream, 'socket_get_status');
}

function fflush_impl($stream): bool
{
    stream_format_validate_impl($stream, 'fflush');
    return \PHP2Go\Internal\stream_flush($stream);
}

function fsync_impl($stream): bool
{
    return stream_sync_impl($stream, 'fsync', false);
}

function fdatasync_impl($stream): bool
{
    return stream_sync_impl($stream, 'fdatasync', true);
}

function stream_sync_impl(
    $stream,
    string $function,
    bool $data_only
): bool {
    stream_format_validate_impl($stream, $function);
    // $status is three-valued and not a success flag: 1 means the stream cannot
    // sync at all, which is the only case that earns the warning below, and 2
    // means it tried and failed.
    $status = \PHP2Go\Internal\stream_sync($stream, $data_only);
    if ($status === 1) {
        \PHP2Go\Internal\warning(
            $function . "(): Can't fsync this stream!"
        );
    }
    return $status === 0;
}

function ftruncate_impl($stream, int $size): bool
{
    if ($size < 0) {
        throw new \ValueError(
            'ftruncate(): Argument #2 ($size) must be greater than or equal to 0'
        );
    }
    stream_format_validate_impl($stream, 'ftruncate');
    return \PHP2Go\Internal\stream_truncate($stream, $size);
}

function fstat_impl($stream): array|false
{
    stream_format_validate_impl($stream, 'fstat');
    $values = \PHP2Go\Internal\stream_stat($stream);
    if ($values === false) {
        return false;
    }
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

function stream_supports_lock_impl($stream): bool
{
    stream_format_validate_impl($stream, 'stream_supports_lock');
    return \PHP2Go\Internal\stream_supports_lock($stream);
}

function stream_socket_validate_impl(
    mixed $stream,
    string $function,
    string $parameter
): void {
    if (!\is_resource($stream)) {
        if (\get_debug_type($stream) === 'resource (closed)') {
            throw new \TypeError(
                $function . '(): supplied resource is not a valid stream resource'
            );
        }
        throw new \TypeError(
            $function . '(): Argument #1 ($' . $parameter .
            ') must be of type resource, ' . \get_debug_type($stream) . ' given'
        );
    }
    if (\get_resource_type($stream) !== 'stream') {
        throw new \TypeError(
            $function . '(): supplied resource is not a valid stream resource'
        );
    }
}

function stream_socket_context_validate_for_open_impl(
    mixed $context,
    string $function,
    int $argument,
    string $parameter
): void {
    if ($context === null) {
        return;
    }
    if (!\is_resource($context)) {
        throw new \TypeError(
            $function . '(): Argument #' . $argument . ' ($' . $parameter .
            ') must be of type resource or null, ' .
            \get_debug_type($context) . ' given'
        );
    }
    if (\get_resource_type($context) !== 'stream-context') {
        throw new \TypeError(
            $function . '(): supplied resource is not a valid Stream-Context resource'
        );
    }
}

function stream_socket_timeout_validate_impl(
    float $timeout,
    string $function,
    int $argument
): void {
    if (!\is_finite($timeout)) {
        throw new \ValueError(
            $function . '(): Argument #' . $argument .
            ' ($timeout) must be a finite value'
        );
    }
}

function stream_socket_open_impl(
    string $function,
    bool $listening,
    string $address,
    string $shown_address,
    int $flags,
    float $timeout,
    mixed $context,
    mixed &$error_code,
    mixed &$error_message
): mixed {
    // Two functions rather than one with a name argument. They take the same
    // four parameters, so the only thing the selector ever carried was which of
    // them to call.
    $result = $listening
        ? \PHP2Go\Internal\stream_socket_server($address, $flags, $timeout, $context)
        : \PHP2Go\Internal\stream_socket_client($address, $flags, $timeout, $context);
    $error_code = $result[2];
    $error_message = $result[3];
    if (!$result[0]) {
        $shown_message = $result[3] === '' ? 'Unknown error' : $result[3];
        \PHP2Go\Internal\warning(
            $function . '(): Unable to connect to ' . $shown_address .
            ' (' . $shown_message . ')'
        );
        return false;
    }
    return $result[1];
}

function stream_socket_server_impl(
    mixed $address,
    mixed &$error_code = null,
    mixed &$error_message = null,
    mixed $flags = STREAM_SERVER_BIND | STREAM_SERVER_LISTEN,
    mixed $context = null
): mixed {
    $address = (string) $address;
    $flags = (int) $flags;
    stream_socket_context_validate_for_open_impl(
        $context,
        'stream_socket_server',
        5,
        'context'
    );
    return stream_socket_open_impl(
        'stream_socket_server',
        true,
        $address,
        $address,
        $flags,
        0.0,
        $context,
        $error_code,
        $error_message
    );
}

function stream_socket_client_impl(
    mixed $address,
    mixed &$error_code = null,
    mixed &$error_message = null,
    mixed $timeout = null,
    mixed $flags = STREAM_CLIENT_CONNECT,
    mixed $context = null
): mixed {
    $address = (string) $address;
    $flags = (int) $flags;
    $timeout = $timeout === null
        ? (float) \ini_get('default_socket_timeout')
        : (float) $timeout;
    stream_socket_timeout_validate_impl($timeout, 'stream_socket_client', 4);
    stream_socket_context_validate_for_open_impl(
        $context,
        'stream_socket_client',
        6,
        'context'
    );
    return stream_socket_open_impl(
        'stream_socket_client',
        false,
        $address,
        $address,
        $flags,
        $timeout,
        $context,
        $error_code,
        $error_message
    );
}

function stream_socket_accept_impl(
    mixed $socket,
    mixed $timeout = null,
    mixed &$peer_name = null
): mixed {
    stream_socket_validate_impl(
        $socket,
        'stream_socket_accept',
        'socket'
    );
    $timeout = $timeout === null
        ? (float) \ini_get('default_socket_timeout')
        : (float) $timeout;
    stream_socket_timeout_validate_impl($timeout, 'stream_socket_accept', 2);
    $result = \PHP2Go\Internal\stream_socket_accept($socket, $timeout);
    if (!$result[0]) {
        $message = $result[3] === '' ? 'Unknown error' : $result[3];
        \PHP2Go\Internal\warning(
            'stream_socket_accept(): Accept failed: ' . $message
        );
        return false;
    }
    $peer_name = $result[2];
    return $result[1];
}

function fsockopen_address_impl(string $hostname, int $port): array
{
    $separator = \strpos($hostname, '://');
    if ($separator !== false) {
        $scheme = \strtolower(\substr($hostname, 0, $separator));
        if (($scheme === 'tcp' || $scheme === 'udp') && $port >= 0) {
            return [$hostname . ':' . $port, $hostname . ':' . $port, null];
        }
        return [$hostname, $hostname, null];
    }
    if ($port < 0) {
        return [
            'tcp://' . $hostname . ':' . $port,
            $hostname . ':' . $port,
            'Failed to parse address "' . $hostname . '"',
        ];
    }
    return [
        'tcp://' . $hostname . ':' . $port,
        $hostname . ':' . $port,
        null,
    ];
}

function fsockopen_common_impl(
    string $function,
    mixed $hostname,
    mixed $port,
    mixed &$error_code,
    mixed &$error_message,
    mixed $timeout,
    bool $persistent
): mixed {
    $hostname = (string) $hostname;
    $port = (int) $port;
    $timeout = $timeout === null
        ? (float) \ini_get('default_socket_timeout')
        : (float) $timeout;
    stream_socket_timeout_validate_impl($timeout, $function, 5);
    [$address, $shown, $forced_error] =
        fsockopen_address_impl($hostname, $port);
    if ($forced_error !== null) {
        $error_code = 0;
        $error_message = $forced_error;
        \PHP2Go\Internal\warning(
            $function . '(): Unable to connect to ' . $shown .
            ' (' . $forced_error . ')'
        );
        return false;
    }
    return stream_socket_open_impl(
        $function,
        false,
        $address,
        $shown,
        STREAM_CLIENT_CONNECT |
            ($persistent ? STREAM_CLIENT_PERSISTENT : 0),
        $timeout,
        null,
        $error_code,
        $error_message
    );
}

function fsockopen_impl(
    mixed $hostname,
    mixed $port = -1,
    mixed &$error_code = null,
    mixed &$error_message = null,
    mixed $timeout = null
): mixed {
    return fsockopen_common_impl(
        'fsockopen',
        $hostname,
        $port,
        $error_code,
        $error_message,
        $timeout,
        false
    );
}

function pfsockopen_impl(
    mixed $hostname,
    mixed $port = -1,
    mixed &$error_code = null,
    mixed &$error_message = null,
    mixed $timeout = null
): mixed {
    return fsockopen_common_impl(
        'pfsockopen',
        $hostname,
        $port,
        $error_code,
        $error_message,
        $timeout,
        true
    );
}
function stream_socket_pair_impl(
    int $domain,
    int $type,
    int $protocol
): array|false {
    $result = \PHP2Go\Internal\stream_socket_pair($domain, $type, $protocol);
    if (!$result[0]) {
        \PHP2Go\Internal\warning(
            'stream_socket_pair(): Failed to create sockets: [' .
            $result[1] . ']: ' . $result[2]
        );
        return false;
    }
    return [$result[1], $result[2]];
}

function stream_socket_get_name_impl(
    mixed $socket,
    bool $remote
): string|false {
    stream_socket_validate_impl(
        $socket,
        'stream_socket_get_name',
        'socket'
    );
    return \PHP2Go\Internal\stream_socket_name($socket, $remote);
}

function stream_socket_recvfrom_impl(
    mixed $socket,
    int $length,
    int $flags = 0,
    mixed &$address = null
): string|false {
    stream_socket_validate_impl(
        $socket,
        'stream_socket_recvfrom',
        'socket'
    );
    if ($length <= 0) {
        throw new \ValueError(
            'stream_socket_recvfrom(): Argument #2 ($length) must be greater than 0'
        );
    }
    $address = null;
    $result = \PHP2Go\Internal\stream_socket_recvfrom($socket, $length, $flags);
    if (!$result[0]) {
        return false;
    }
    $address = $result[2];
    return $result[1];
}

function stream_socket_sendto_impl(
    mixed $socket,
    string $data,
    int $flags = 0,
    string $address = ''
): int|false {
    stream_socket_validate_impl(
        $socket,
        'stream_socket_sendto',
        'socket'
    );
    if ($address !== '') {
        $result = \PHP2Go\Internal\stream_socket_sendto_address(
            $socket,
            $data,
            $address,
            $flags
        );
        if (!$result[0]) {
            $nul = \strpos($address, "\0");
            $shown = $nul === false ? $address : \substr($address, 0, $nul);
            if (\str_starts_with($result[3], 'Failed to parse address')) {
                \PHP2Go\Internal\warning(
                    "stream_socket_sendto(): Failed to parse `" . $shown .
                    "' into a valid network address"
                );
            } else {
                \PHP2Go\Internal\warning(
                    'stream_socket_sendto(): ' .
                    ($result[3] === '' ? 'Unknown error' : $result[3])
                );
            }
            return false;
        }
        return $result[1];
    }
    return \PHP2Go\Internal\stream_socket_sendto($socket, $data, $flags);
}

function stream_socket_shutdown_impl(
    mixed $stream,
    int $mode
): bool {
    stream_socket_validate_impl(
        $stream,
        'stream_socket_shutdown',
        'stream'
    );
    if ($mode < STREAM_SHUT_RD || $mode > STREAM_SHUT_RDWR) {
        throw new \ValueError(
            'stream_socket_shutdown(): Argument #2 ($mode) must be one of ' .
            'STREAM_SHUT_RD, STREAM_SHUT_WR, or STREAM_SHUT_RDWR'
        );
    }
    return \PHP2Go\Internal\stream_socket_shutdown($stream, $mode);
}

function stream_socket_enable_crypto_impl(
    mixed $stream,
    bool $enable,
    ?int $crypto_method = null,
    mixed $session_stream = null
): int|bool {
    stream_socket_validate_impl(
        $stream,
        'stream_socket_enable_crypto',
        'stream'
    );
    if ($session_stream !== null && !\is_resource($session_stream)) {
        if (\get_debug_type($session_stream) === 'resource (closed)') {
            throw new \TypeError(
                'stream_socket_enable_crypto(): supplied resource is not a valid stream resource'
            );
        }
        throw new \TypeError(
            'stream_socket_enable_crypto(): Argument #4 ($session_stream) ' .
            'must be of type resource or null, ' .
            \get_debug_type($session_stream) . ' given'
        );
    }
    if ($enable && $crypto_method === null) {
        throw new \ValueError(
            'stream_socket_enable_crypto(): Argument #3 ($crypto_method) ' .
            'must be specified when enabling encryption'
        );
    }
    if (
        $enable && $session_stream !== null &&
        \get_resource_type($session_stream) !== 'stream'
    ) {
        throw new \TypeError(
            'stream_socket_enable_crypto(): supplied resource is not a valid stream resource'
        );
    }
    \PHP2Go\Internal\warning(
        'stream_socket_enable_crypto(): This stream does not support SSL/crypto'
    );
    return !$enable;
}

function stream_select_scan_impl(
    ?array $streams,
    int &$valid,
    ?string &$invalid
): void {
    if ($streams === null) {
        return;
    }
    foreach ($streams as $stream) {
        if (\is_resource($stream) && \get_resource_type($stream) === 'stream') {
            $valid++;
            continue;
        }
        if ($invalid === null) {
            $invalid = \is_resource($stream) ||
                \get_debug_type($stream) === 'resource (closed)'
                ? 'resource'
                : 'argument';
        }
    }
}

function stream_select_impl(
    ?array &$read,
    ?array &$write,
    ?array &$except,
    ?int $seconds,
    ?int $microseconds = null
): int|false {
    $valid = 0;
    $invalid = null;
    stream_select_scan_impl($read, $valid, $invalid);
    stream_select_scan_impl($write, $valid, $invalid);
    stream_select_scan_impl($except, $valid, $invalid);

    if ($valid === 0) {
        throw new \ValueError('No stream arrays were passed');
    }
    if ($invalid !== null) {
        throw new \TypeError(
            'stream_select(): supplied ' . $invalid .
            ' is not a valid stream resource'
        );
    }
    if ($seconds === null && $microseconds !== null && $microseconds !== 0) {
        throw new \ValueError(
            'stream_select(): Argument #5 ($microseconds) must be null ' .
            'when argument #4 ($seconds) is null'
        );
    }
    if ($seconds !== null && $seconds < 0) {
        throw new \ValueError(
            'stream_select(): Argument #4 ($seconds) must be greater than ' .
            'or equal to 0'
        );
    }
    if ($microseconds !== null && $microseconds < 0) {
        throw new \ValueError(
            'stream_select(): Argument #5 ($microseconds) must be greater ' .
            'than or equal to 0'
        );
    }

    $result = \PHP2Go\Internal\stream_select(
        $read,
        $write,
        $except,
        $seconds,
        $microseconds
    );
    if ($result === false) {
        return false;
    }
    if ($read !== null) {
        $read = $result[1];
    }
    if ($write !== null) {
        $write = $result[2];
    }
    if ($except !== null) {
        $except = $result[3];
    }
    return $result[0];
}

function flock_impl(
    $stream,
    int $operation,
    &$would_block = null
): bool {
    stream_format_validate_impl($stream, 'flock');
    $action = $operation & 3;
    if ($action < 1 || $action > 3) {
        throw new \ValueError(
            'flock(): Argument #2 ($operation) must be one of ' .
            'LOCK_SH, LOCK_EX, or LOCK_UN'
        );
    }
    $result = \PHP2Go\Internal\stream_lock($stream, $operation);
    $would_block = $result[1];
    return $result[0];
}

function fgetc_impl($stream): string|false
{
    $value = \fread($stream, 1);
    return $value === '' ? false : $value;
}

function fpassthru_impl($stream): int
{
    $read = 0;
    while (true) {
        $chunk = \fread($stream, 8192);
        if ($chunk === '' || $chunk === false) {
            break;
        }
        echo $chunk;
        $read += \strlen($chunk);
    }

    return $read;
}

function fprintf_impl($stream, string $format, mixed ...$values): int
{
    stream_format_validate_impl($stream, 'fprintf');
    $formatted = \PHP2Go\Internal\formatted_print(
        $format,
        $values,
        2,
        false
    );
    \fwrite($stream, $formatted);
    return \strlen($formatted);
}

function vfprintf_impl($stream, string $format, array $values): int
{
    stream_format_validate_impl($stream, 'vfprintf');
    $formatted = \PHP2Go\Internal\formatted_print(
        $format,
        $values,
        0,
        true
    );
    \fwrite($stream, $formatted);
    return \strlen($formatted);
}

function stream_format_validate_impl($stream, string $function): void
{
    if (!\is_resource($stream)) {
        if (\get_debug_type($stream) === 'resource (closed)') {
            throw new \TypeError(
                $function . '(): supplied resource is not a valid stream resource'
            );
        }
        throw new \TypeError(
            $function . '(): Argument #1 ($stream) must be of type resource, ' .
            \get_debug_type($stream) . ' given'
        );
    }
    if (\get_resource_type($stream) !== 'stream') {
        throw new \TypeError(
            $function . '(): supplied resource is not a valid stream resource'
        );
    }
}
function csv_validate_impl(
    string $function,
    string $value,
    int $argument,
    string $name,
    bool $allow_empty = false
): string {
    $length = \strlen($value);
    if ($length !== 1 && (!$allow_empty || $length !== 0)) {
        throw new \ValueError(
            $function . '(): Argument #' . $argument . ' ($' . $name . ') must be ' .
            ($allow_empty ? 'empty or a single character' : 'a single character')
        );
    }
    return $value;
}

function csv_escape_impl(
    string $function,
    string $escape,
    int $argument,
    bool $provided
): string {
    if (!$provided) {
        \PHP2Go\Internal\deprecated(
            $function . '(): the $escape parameter must be provided as its default value will change'
        );
    }
    return csv_validate_impl($function, $escape, $argument, 'escape', true);
}

function csv_space_impl(string $byte): bool
{
    return $byte === ' ' || $byte === "\t" || $byte === "\n" ||
        $byte === "\r" || $byte === "\v" || $byte === "\f";
}

function csv_parse_impl(
    string $input,
    string $separator,
    string $enclosure,
    string $escape,
    bool &$unterminated
): ?array {
    $length = \strlen($input);
    $limit = $length;
    $line_ending = '';
    if ($limit > 0 && $input[$limit - 1] === "\n") {
        $line_ending = "\n";
        $limit--;
        if ($limit > 0 && $input[$limit - 1] === "\r") {
            $line_ending = "\r\n";
            $limit--;
        }
    } elseif ($limit > 0 && $input[$limit - 1] === "\r") {
        $line_ending = "\r";
        $limit--;
    }

    $unterminated = false;
    $fields = [];
    $offset = 0;
    $first = true;
    do {
        $separated = false;
        if ($offset < $limit) {
            $probe = $offset;
            while (
                $probe < $limit &&
                $input[$probe] !== $separator &&
                csv_space_impl($input[$probe])
            ) {
                $probe++;
            }
            if ($probe < $limit && $input[$probe] === $enclosure) {
                $offset = $probe;
            }
        }

        if ($first && $offset === $limit) {
            return null;
        }
        $first = false;

        if ($offset < $limit && $input[$offset] === $enclosure) {
            $offset++;
            $field = '';
            $closed = false;
            while ($offset < $limit) {
                $byte = $input[$offset];
                if ($byte === $enclosure) {
                    if ($offset + 1 < $limit && $input[$offset + 1] === $enclosure) {
                        $field .= $enclosure;
                        $offset += 2;
                        continue;
                    }
                    $offset++;
                    $closed = true;
                    break;
                }
                if ($escape !== '' && $byte === $escape) {
                    $field .= $byte;
                    $offset++;
                    if ($offset < $limit) {
                        $field .= $input[$offset];
                        $offset++;
                    }
                    continue;
                }
                $field .= $byte;
                $offset++;
            }

            if (!$closed) {
                $field .= $line_ending;
                $unterminated = true;
            } else {
                while ($offset < $limit && $input[$offset] !== $separator) {
                    $field .= $input[$offset];
                    $offset++;
                }
            }
            if ($offset < $limit && $input[$offset] === $separator) {
                $offset++;
                $separated = true;
            }
            $fields[] = $field;
        } else {
            $start = $offset;
            while ($offset < $limit && $input[$offset] !== $separator) {
                $offset++;
            }
            $fields[] = \substr($input, $start, $offset - $start);
            if ($offset < $limit && $input[$offset] === $separator) {
                $offset++;
                $separated = true;
            }
        }
    } while ($separated);

    return $fields;
}

function csv_read_line_impl($stream, ?int $length): string|false
{
    $line = '';
    while ($length === null || \strlen($line) < $length) {
        $byte = \fread($stream, 1);
        if ($byte === false || $byte === '') {
            return $line === '' ? false : $line;
        }
        $line .= $byte;
        if ($byte === "\n") {
            break;
        }
    }
    return $line;
}

function str_getcsv_impl(
    string $string,
    string $separator = ',',
    string $enclosure = '"',
    string $escape = '\\'
): array {
    $separator = csv_validate_impl('str_getcsv', $separator, 2, 'separator');
    $enclosure = csv_validate_impl('str_getcsv', $enclosure, 3, 'enclosure');
    $escape = csv_escape_impl('str_getcsv', $escape, 4, \func_num_args() >= 4);
    $unterminated = false;
    $fields = csv_parse_impl($string, $separator, $enclosure, $escape, $unterminated);
    return $fields ?? [null];
}

function fgetcsv_impl(
    $stream,
    ?int $length = null,
    string $separator = ',',
    string $enclosure = '"',
    string $escape = '\\'
): array|false {
    $separator = csv_validate_impl('fgetcsv', $separator, 3, 'separator');
    $enclosure = csv_validate_impl('fgetcsv', $enclosure, 4, 'enclosure');
    $escape = csv_escape_impl('fgetcsv', $escape, 5, \func_num_args() >= 5);
    if ($length !== null && ($length < 0 || $length >= PHP_INT_MAX)) {
        throw new \ValueError(
            'fgetcsv(): Argument #2 ($length) must be between 0 and ' . (PHP_INT_MAX - 1)
        );
    }
    stream_format_validate_impl($stream, 'fgetcsv');

    $line = csv_read_line_impl($stream, $length === null || $length === 0 ? null : $length);
    if ($line === false) {
        return false;
    }
    while (true) {
        $unterminated = false;
        $fields = csv_parse_impl($line, $separator, $enclosure, $escape, $unterminated);
        if (!$unterminated) {
            return $fields ?? [null];
        }
        $next = csv_read_line_impl($stream, null);
        if ($next === false) {
            return $fields ?? [null];
        }
        $line .= $next;
    }
}

function csv_contains_impl(string $value, string $needle): bool
{
    for ($index = 0, $length = \strlen($value); $index < $length; $index++) {
        if ($value[$index] === $needle) {
            return true;
        }
    }
    return false;
}

function fputcsv_impl(
    $stream,
    array $fields,
    string $separator = ',',
    string $enclosure = '"',
    string $escape = '\\',
    string $eol = "\n"
): int|false {
    $separator = csv_validate_impl('fputcsv', $separator, 3, 'separator');
    $enclosure = csv_validate_impl('fputcsv', $enclosure, 4, 'enclosure');
    $escape = csv_escape_impl('fputcsv', $escape, 5, \func_num_args() >= 5);
    stream_format_validate_impl($stream, 'fputcsv');

    $line = '';
    $first = true;
    foreach ($fields as $value) {
        if (!$first) {
            $line .= $separator;
        }
        $first = false;
        $field = (string)$value;
        $quote = csv_contains_impl($field, $separator) ||
            csv_contains_impl($field, $enclosure) ||
            ($escape !== '' && csv_contains_impl($field, $escape)) ||
            csv_contains_impl($field, "\n") || csv_contains_impl($field, "\r") ||
            csv_contains_impl($field, "\t") || csv_contains_impl($field, ' ');
        if (!$quote) {
            $line .= $field;
            continue;
        }

        $line .= $enclosure;
        $escaped = false;
        for ($index = 0, $field_length = \strlen($field); $index < $field_length; $index++) {
            $byte = $field[$index];
            if ($escape !== '' && $byte === $escape) {
                $escaped = true;
            } elseif (!$escaped && $byte === $enclosure) {
                $line .= $enclosure;
            } else {
                $escaped = false;
            }
            $line .= $byte;
        }
        $line .= $enclosure;
    }
    $line .= $eol;
    return \fwrite($stream, $line);
}

function stream_get_line_impl($stream, int $length, string $ending = ''): string|false
{
    stream_format_validate_impl($stream, 'stream_get_line');
    if ($length < 0) {
        throw new \ValueError(
            'stream_get_line(): Argument #2 ($length) must be greater than or equal to 0'
        );
    }

    $result = '';
    $ending_length = \strlen($ending);
    while ($length === 0 || \strlen($result) < $length) {
        $byte = \fread($stream, 1);
        if ($byte === false) {
            return $result === '' ? false : $result;
        }
        if ($byte === '') {
            return $result === '' ? false : $result;
        }
        $result .= $byte;
        if (
            $ending_length > 0 &&
            \strlen($result) >= $ending_length &&
            \substr($result, -$ending_length) === $ending
        ) {
            return \substr($result, 0, -$ending_length);
        }
    }
    return $result;
}

function stream_get_contents_impl($stream, ?int $length = null, int $offset = -1): string|false
{
    if ($length !== null && $length < -1) {
        throw new \ValueError('stream_get_contents(): Argument #2 ($length) must be greater than or equal to -1');
    }

    if ($offset >= 0 && \fseek($stream, $offset) !== 0) {
        return false;
    }

    if ($length === 0) {
        return '';
    }

    $remaining = $length === -1 ? null : $length;
    $result = '';
    if ($remaining === null) {
        // The streams layer asks the wrapper for a size hint before an
        // unbounded read. The hint is optional, but the stream_stat callback
        // is observable to a registered user-space wrapper.
        \fstat($stream);
    }
    while ($remaining === null || $remaining > 0) {
        $amount = $remaining === null || $remaining > 8192 ? 8192 : $remaining;
        $chunk = \fread($stream, $amount);
        if ($chunk === false) {
            return false;
        }
        $result .= $chunk;
        if ($chunk === '') {
            break;
        }
        if ($remaining !== null) {
            $remaining -= \strlen($chunk);
        }
    }

    return $result;
}

function stream_copy_to_stream_impl($from, $to, ?int $length = null, int $offset = 0): int|false
{
    if ($offset > 0 && \fseek($from, $offset) !== 0) {
        return false;
    }

    $remaining = $length === null || $length < 0 ? null : $length;
    $copied = 0;
    while ($remaining === null || $remaining > 0) {
        $amount = $remaining === null || $remaining > 8192 ? 8192 : $remaining;
        $chunk = \fread($from, $amount);
        if ($chunk === false) {
            return false;
        }
        if ($chunk === '') {
            break;
        }

        $chunkLength = \strlen($chunk);
        $at = 0;
        while ($at < $chunkLength) {
            $written = \fwrite($to, \substr($chunk, $at));
            if ($written === false) {
                return false;
            }
            if ($written === 0) {
                return $copied;
            }
            $at += $written;
            $copied += $written;
        }

        if ($remaining !== null) {
            $remaining -= $chunkLength;
        }
    }

    return $copied;
}


function fscanf_impl(
    $stream,
    string $format,
    mixed &...$vars
): array|int|false|null {
    fscanf_validate_stream_impl($stream);
    $line = \fgets($stream);
    if ($line === false) {
        return false;
    }
    return \PHP2Go\Extension\Standard\scanf_impl(
        $line,
        $format,
        $vars
    );
}

function fscanf_validate_stream_impl($stream): void
{
    if (!\is_resource($stream)) {
        if (\get_debug_type($stream) === 'resource (closed)') {
            throw new \TypeError(
                'fscanf(): supplied resource is not a valid ' .
                'File-Handle resource'
            );
        }
        throw new \TypeError(
            'fscanf(): Argument #1 ($stream) must be of type resource, ' .
            \get_debug_type($stream) . ' given'
        );
    }
    if (\get_resource_type($stream) !== 'stream') {
        throw new \TypeError(
            'fscanf(): supplied resource is not a valid ' .
            'File-Handle resource'
        );
    }
}

/**
 * `php://memory` and `php://temp` as a stream wrapper: a byte string and a
 * position, driven through the same protocol a class passed to
 * stream_wrapper_register() is. The streams layer presents the stream as the
 * engine's own -- `stream_type` MEMORY or TEMP, no wrapper_data -- so only
 * its behaviour lives here, measured against the engine:
 *
 * - a mode without `w`, `a` or `+` is read-only: writes and truncation fail,
 *   and fstat's mode is 0100444 rather than 0100666;
 * - `a` makes every write land at the end;
 * - seeking past the end is allowed, and a write there fills the gap with
 *   NUL bytes; seeking before the start is refused;
 * - fstat reports device 12, one link, no rdev/blksize/blocks, and times 0;
 * - EOF is set by a read that finds nothing, survives a write, and is
 *   cleared by a seek -- a read that empties the buffer does not set it.
 *
 * php://temp spills to a file beyond its memory limit in the engine, which is
 * not observable here, so the two share this class.
 */
final class MemoryStreamWrapper
{
    public $context;

    private string $data = '';
    private int $position = 0;
    private bool $readOnly = false;
    private bool $append = false;
    private bool $eof = false;

    public function stream_open(string $path, string $mode, int $options, ?string &$opened_path): bool
    {
        $this->readOnly = \strpbrk($mode, 'wa+') === false;
        $this->append = \strpos($mode, 'a') !== false;
        return true;
    }

    public function stream_read(int $count): string
    {
        if ($this->position >= \strlen($this->data)) {
            $this->eof = true;
            return '';
        }
        $chunk = (string) \substr($this->data, $this->position, $count);
        $this->position += \strlen($chunk);
        return $chunk;
    }

    public function stream_write(string $bytes): int|false
    {
        if ($this->readOnly) {
            return false;
        }
        $size = \strlen($this->data);
        if ($this->append) {
            $this->position = $size;
        }
        if ($this->position === $size) {
            $this->data .= $bytes;
        } else {
            if ($this->position > $size) {
                $this->data .= \str_repeat("\0", $this->position - $size);
            }
            $this->data = \substr($this->data, 0, $this->position) . $bytes
                . (string) \substr($this->data, $this->position + \strlen($bytes));
        }
        $this->position += \strlen($bytes);
        return \strlen($bytes);
    }

    public function stream_eof(): bool
    {
        return $this->eof;
    }

    public function stream_tell(): int
    {
        return $this->position;
    }

    public function stream_seek(int $offset, int $whence): bool
    {
        if ($whence === \SEEK_SET) {
            $base = 0;
        } elseif ($whence === \SEEK_CUR) {
            $base = $this->position;
        } elseif ($whence === \SEEK_END) {
            $base = \strlen($this->data);
        } else {
            return false;
        }
        if ($base + $offset < 0) {
            return false;
        }
        $this->position = $base + $offset;
        $this->eof = false;
        return true;
    }

    public function stream_truncate(int $size): bool
    {
        if ($this->readOnly || $size < 0) {
            return false;
        }
        if (\strlen($this->data) >= $size) {
            $this->data = \substr($this->data, 0, $size);
        } else {
            $this->data .= \str_repeat("\0", $size - \strlen($this->data));
        }
        return true;
    }

    public function stream_stat(): array
    {
        return [
            'dev' => 12, 'ino' => 0, 'mode' => $this->readOnly ? 0100444 : 0100666,
            'nlink' => 1, 'uid' => 0, 'gid' => 0, 'rdev' => -1,
            'size' => \strlen($this->data), 'atime' => 0, 'mtime' => 0, 'ctime' => 0,
            'blksize' => -1, 'blocks' => -1,
        ];
    }

    public function stream_flush(): bool
    {
        return true;
    }

    public function stream_close(): void
    {
    }
}
