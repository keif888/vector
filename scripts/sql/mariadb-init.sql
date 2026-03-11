CREATE DATABASE IF NOT EXISTS `develop`;
CREATE USER IF NOT EXISTS 'develop'@'%' IDENTIFIED BY 'develop';
GRANT ALL PRIVILEGES ON `develop`.* TO 'develop'@'%';

CREATE DATABASE IF NOT EXISTS `testing`;
CREATE USER IF NOT EXISTS 'testing'@'%' IDENTIFIED BY 'testing';
GRANT ALL PRIVILEGES ON `testing`.* TO 'testing'@'%';
