package com.s3bender.repository;

import com.s3bender.model.BucketEntity;
import org.springframework.data.jpa.repository.JpaRepository;

import java.util.Optional;

public interface BucketRepository extends JpaRepository<BucketEntity, String> {

    Optional<BucketEntity> findByAccessKey(String accessKey);
}
