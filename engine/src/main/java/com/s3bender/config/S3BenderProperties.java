package com.s3bender.config;

import org.springframework.boot.context.properties.ConfigurationProperties;

@ConfigurationProperties(prefix = "s3bender")
public class S3BenderProperties {

    private final Storage storage = new Storage();
    private final Auth auth = new Auth();
    private final Signing signing = new Signing();

    /** Optional override for the host[:port] used in presigned URLs, e.g. when behind a reverse proxy. */
    private String publicBaseUrl;

    public String getPublicBaseUrl() {
        return publicBaseUrl;
    }

    public void setPublicBaseUrl(String publicBaseUrl) {
        this.publicBaseUrl = publicBaseUrl;
    }

    public Storage getStorage() {
        return storage;
    }

    public Auth getAuth() {
        return auth;
    }

    public Signing getSigning() {
        return signing;
    }

    public static class Storage {
        /** Filesystem directory under which every bucket gets its own subdirectory. */
        private String root = "./data/objects";

        public String getRoot() {
            return root;
        }

        public void setRoot(String root) {
            this.root = root;
        }
    }

    public static class Auth {
        /** Required via X-Admin-Api-Key to create/delete/list buckets. */
        private String adminApiKey;

        /** Base64-encoded 32-byte AES key used to encrypt per-bucket secret keys at rest. */
        private String masterKey;

        public String getAdminApiKey() {
            return adminApiKey;
        }

        public void setAdminApiKey(String adminApiKey) {
            this.adminApiKey = adminApiKey;
        }

        public String getMasterKey() {
            return masterKey;
        }

        public void setMasterKey(String masterKey) {
            this.masterKey = masterKey;
        }
    }

    public static class Signing {
        private long clockSkewSeconds = 900;
        private long maxPresignExpirySeconds = 604800;

        public long getClockSkewSeconds() {
            return clockSkewSeconds;
        }

        public void setClockSkewSeconds(long clockSkewSeconds) {
            this.clockSkewSeconds = clockSkewSeconds;
        }

        public long getMaxPresignExpirySeconds() {
            return maxPresignExpirySeconds;
        }

        public void setMaxPresignExpirySeconds(long maxPresignExpirySeconds) {
            this.maxPresignExpirySeconds = maxPresignExpirySeconds;
        }
    }
}
