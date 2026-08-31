using Xunit;

// The integration tests drive a real WebApplicationFactory host that reads its storage root, DB
// path, and keys from process-wide environment variables, and share one on-disk SQLite file per
// host. Running test classes in parallel would let one class's env-var setup clobber another's
// mid-run, so the whole assembly runs serially.
[assembly: CollectionBehavior(DisableTestParallelization = true)]
