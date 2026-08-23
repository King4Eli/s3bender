package com.s3bender.service;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class SignatureServiceTest {

    private final SignatureService signatureService = new SignatureService();

    @Test
    void sameInputsProduceMatchingSignature() {
        String stringToSign = signatureService.stringToSignForHeader("GET", "/buckets/demo/objects/a.txt", 1_700_000_000L);
        String signature = signatureService.sign("top-secret", stringToSign);

        String recomputed = signatureService.sign("top-secret", stringToSign);
        assertTrue(signatureService.matches(signature, recomputed));
    }

    @Test
    void differentSecretProducesMismatch() {
        String stringToSign = signatureService.stringToSignForPresign("GET", "/buckets/demo/objects/a.txt", 1_700_000_000L);
        String signature = signatureService.sign("secret-a", stringToSign);
        String other = signatureService.sign("secret-b", stringToSign);

        assertFalse(signatureService.matches(signature, other));
    }

    @Test
    void differentPathProducesMismatch() {
        String secret = "top-secret";
        String signatureForA = signatureService.sign(secret,
                signatureService.stringToSignForHeader("GET", "/buckets/demo/objects/a.txt", 1_700_000_000L));
        String signatureForB = signatureService.sign(secret,
                signatureService.stringToSignForHeader("GET", "/buckets/demo/objects/b.txt", 1_700_000_000L));

        assertFalse(signatureService.matches(signatureForA, signatureForB));
    }
}
