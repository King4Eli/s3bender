package com.s3bender.service;

import org.springframework.stereotype.Service;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.HexFormat;

/**
 * HMAC-SHA256 request signing, shared by header-based auth and presigned URLs.
 * See /_docs/auth-and-signing.md for the exact string-to-sign layout.
 */
@Service
public class SignatureService {

    private static final String HMAC_ALGO = "HmacSHA256";

    public String sign(String secretKey, String stringToSign) {
        try {
            Mac mac = Mac.getInstance(HMAC_ALGO);
            mac.init(new SecretKeySpec(secretKey.getBytes(StandardCharsets.UTF_8), HMAC_ALGO));
            byte[] raw = mac.doFinal(stringToSign.getBytes(StandardCharsets.UTF_8));
            return HexFormat.of().formatHex(raw);
        } catch (Exception e) {
            throw new IllegalStateException("Failed to compute HMAC signature", e);
        }
    }

    /** Constant-time comparison to avoid leaking signature material via timing. */
    public boolean matches(String expectedHex, String providedHex) {
        if (expectedHex == null || providedHex == null) {
            return false;
        }
        byte[] a = expectedHex.getBytes(StandardCharsets.UTF_8);
        byte[] b = providedHex.getBytes(StandardCharsets.UTF_8);
        return MessageDigest.isEqual(a, b);
    }

    public String stringToSignForHeader(String method, String path, long timestampEpochSeconds) {
        return method + "\n" + path + "\n" + timestampEpochSeconds;
    }

    public String stringToSignForPresign(String method, String path, long expiresEpochSeconds) {
        return method + "\n" + path + "\n" + expiresEpochSeconds;
    }
}
