-- This file is used by docker-compose to initialize the database
-- The actual schema is managed by migrations in the migrations/ directory

-- Create the database if it doesn't exist (though docker-compose handles this)
-- This file can be used for any additional initialization if needed

-- Set timezone
SET timezone = 'UTC';

