SELECT 'CREATE USER develop PASSWORD ''develop'''
WHERE NOT EXISTS (SELECT FROM pg_user WHERE usename = 'develop')\gexec
SELECT 'CREATE DATABASE develop OWNER develop'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'develop')\gexec

SELECT 'CREATE USER testing PASSWORD ''testing'''
WHERE NOT EXISTS (SELECT FROM pg_user WHERE usename = 'testing')\gexec
SELECT 'CREATE DATABASE testing OWNER testing'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'testing')\gexec
