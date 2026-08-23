package com.s3bender.service;

import com.s3bender.config.S3BenderProperties;
import jakarta.annotation.PostConstruct;
import org.springframework.stereotype.Service;

import javax.crypto.Cipher;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.Base64;

/**
 * Encrypts per-bucket secret keys at rest (AES-256-GCM) using a master key supplied out of band
 * (S3BENDER_MASTER_KEY), and generates the random access/secret key pairs issued on bucket creation.
 */
@Service
public class CryptoService {

    private static final String ALGO = "AES/GCM/NoPadding";
    private static final int GCM_TAG_BITS = 128;
    private static final int GCM_IV_BYTES = 12;

    private final SecureRandom random = new SecureRandom();
    private final S3BenderProperties properties;
    private SecretKey masterKey;

    public CryptoService(S3BenderProperties properties) {
        this.properties = properties;
    }

    @PostConstruct
    void init() {
        String encoded = properties.getAuth().getMasterKey();
        if (encoded == null || encoded.isBlank()) {
            throw new IllegalStateException(
                    "S3BENDER_MASTER_KEY is not set. Generate one with: openssl rand -base64 32");
        }
        byte[] keyBytes = Base64.getDecoder().decode(encoded);
        if (keyBytes.length != 32) {
            throw new IllegalStateException("S3BENDER_MASTER_KEY must decode to exactly 32 bytes (AES-256)");
        }
        this.masterKey = new SecretKeySpec(keyBytes, "AES");
    }

    public String encryptSecret(String plaintext) {
        try {
            byte[] iv = new byte[GCM_IV_BYTES];
            random.nextBytes(iv);
            Cipher cipher = Cipher.getInstance(ALGO);
            cipher.init(Cipher.ENCRYPT_MODE, masterKey, new GCMParameterSpec(GCM_TAG_BITS, iv));
            byte[] cipherText = cipher.doFinal(plaintext.getBytes(StandardCharsets.UTF_8));

            ByteBuffer buffer = ByteBuffer.allocate(iv.length + cipherText.length);
            buffer.put(iv).put(cipherText);
            return Base64.getEncoder().encodeToString(buffer.array());
        } catch (Exception e) {
            throw new IllegalStateException("Failed to encrypt secret key", e);
        }
    }

    public String decryptSecret(String encoded) {
        try {
            byte[] raw = Base64.getDecoder().decode(encoded);
            ByteBuffer buffer = ByteBuffer.wrap(raw);
            byte[] iv = new byte[GCM_IV_BYTES];
            buffer.get(iv);
            byte[] cipherText = new byte[buffer.remaining()];
            buffer.get(cipherText);

            Cipher cipher = Cipher.getInstance(ALGO);
            cipher.init(Cipher.DECRYPT_MODE, masterKey, new GCMParameterSpec(GCM_TAG_BITS, iv));
            return new String(cipher.doFinal(cipherText), StandardCharsets.UTF_8);
        } catch (Exception e) {
            throw new IllegalStateException("Failed to decrypt secret key", e);
        }
    }

    public String generateAccessKey() {
        return "AK" + randomBase32(16);
    }

    public String generateSecretKey() {
        return randomBase32(32);
    }

    private String randomBase32(int bytesLength) {
        byte[] bytes = new byte[bytesLength];
        random.nextBytes(bytes);
        return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
    }
}
