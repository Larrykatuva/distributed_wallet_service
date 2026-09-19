-- Wallets mutated by the original code carry a checksum in the old, malformed
-- format (built with a wrong printf verb), which migration 000005 skipped
-- because it only stamped empty checksums. Those wallets fail every integrity
-- check. Re-stamp all wallets once with the current formula
-- (actors.ComputeChecksum): sha256("id:actual:available:processing:version").
UPDATE wallets
SET checksum = encode(
        sha256(convert_to(
            id::text || ':' || actual_balance || ':' || available_balance || ':' || processing_balance || ':' || version,
            'UTF8')),
        'hex')
WHERE checksum <> encode(
        sha256(convert_to(
            id::text || ':' || actual_balance || ':' || available_balance || ':' || processing_balance || ':' || version,
            'UTF8')),
        'hex');
