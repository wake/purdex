-- wwdctl: everything the wwd administrator account may do on the MySQL dev instance of mserv (WWD-4d-5; docs/wwd-deploy.md).
--
-- MySQL has no way to limit CREATE USER, KILL or the process list to names with a prefix: those privileges are global. So the
-- account `wwd_admin` holds no global privilege at all, only EXECUTE on this database, and every routine here runs as root
-- (SQL SECURITY DEFINER) after checking its arguments against the exact shapes internal/wwd/jobdb makes:
--
--   database  wwd_  + 32 lower-case hex          user  wwdu_ + 26 lower-case hex        (the user is the first 26 hex of its database)
--   password  48 lower-case hex                  host  letters, digits and . _ : -       (no wildcard)
--
-- Every check is anchored at both ends (\A ... \z; ICU's $ would also match before a final newline), case sensitive (REGEXP_LIKE ... 'c') and
-- length checked, and the name that reaches a statement is only ever one that passed. Install and remove: scripts/wwd-mysql-setup.sh.

CREATE DATABASE IF NOT EXISTS wwdctl;

DELIMITER $$

DROP PROCEDURE IF EXISTS wwdctl.wwd_create_db$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_create_db(IN p_db VARCHAR(64))
  SQL SECURITY DEFINER
BEGIN
  IF p_db IS NULL OR CHAR_LENGTH(p_db) <> 36 OR NOT REGEXP_LIKE(p_db, '\\Awwd_[0-9a-f]{32}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_create_db: not a wwd database name';
  END IF;
  SET @wwdctl_sql = CONCAT('CREATE DATABASE IF NOT EXISTS `', p_db, '`');
  PREPARE wwdctl_s FROM @wwdctl_sql;
  EXECUTE wwdctl_s;
  DEALLOCATE PREPARE wwdctl_s;
END$$

DROP PROCEDURE IF EXISTS wwdctl.wwd_create_user$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_create_user(IN p_user VARCHAR(32), IN p_pass VARCHAR(64), IN p_host VARCHAR(64), IN p_db VARCHAR(64))
  SQL SECURITY DEFINER
BEGIN
  IF p_db IS NULL OR CHAR_LENGTH(p_db) <> 36 OR NOT REGEXP_LIKE(p_db, '\\Awwd_[0-9a-f]{32}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_create_user: not a wwd database name';
  END IF;
  IF p_user IS NULL OR CHAR_LENGTH(p_user) <> 31 OR NOT REGEXP_LIKE(p_user, '\\Awwdu_[0-9a-f]{26}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_create_user: not a wwd user name';
  END IF;
  -- the user may only be given the database of its own hash
  IF SUBSTRING(p_db, 5, 26) <> SUBSTRING(p_user, 6, 26) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_create_user: the user does not belong to that database';
  END IF;
  IF p_pass IS NULL OR CHAR_LENGTH(p_pass) <> 48 OR NOT REGEXP_LIKE(p_pass, '\\A[0-9a-f]{48}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_create_user: not a wwd password';
  END IF;
  IF p_host IS NULL OR CHAR_LENGTH(p_host) > 64 OR NOT REGEXP_LIKE(p_host, '\\A[A-Za-z0-9_.:-]{1,64}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_create_user: not a plain host';
  END IF;
  SET @wwdctl_sql = CONCAT('CREATE USER ''', p_user, '''@''', p_host, ''' IDENTIFIED BY ''', p_pass, '''');
  PREPARE wwdctl_s FROM @wwdctl_sql;
  EXECUTE wwdctl_s;
  DEALLOCATE PREPARE wwdctl_s;
  -- all privileges on that one database; no GRANT OPTION, nothing global
  SET @wwdctl_sql = CONCAT('GRANT ALL PRIVILEGES ON `', p_db, '`.* TO ''', p_user, '''@''', p_host, '''');
  PREPARE wwdctl_s FROM @wwdctl_sql;
  EXECUTE wwdctl_s;
  DEALLOCATE PREPARE wwdctl_s;
END$$

-- Ends the open connections of one wwd user and answers how many are still listed (the caller wants 0).
DROP PROCEDURE IF EXISTS wwdctl.wwd_kill_user$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_kill_user(IN p_user VARCHAR(32))
  SQL SECURITY DEFINER
BEGIN
  DECLARE v_done INT DEFAULT 0;
  DECLARE v_id BIGINT UNSIGNED;
  DECLARE c CURSOR FOR SELECT id FROM information_schema.processlist WHERE user = p_user AND id <> CONNECTION_ID();
  DECLARE CONTINUE HANDLER FOR NOT FOUND SET v_done = 1;
  IF p_user IS NULL OR CHAR_LENGTH(p_user) <> 31 OR NOT REGEXP_LIKE(p_user, '\\Awwdu_[0-9a-f]{26}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_kill_user: not a wwd user name';
  END IF;
  OPEN c;
  kill_loop: LOOP
    FETCH c INTO v_id;
    IF v_done = 1 THEN
      LEAVE kill_loop;
    END IF;
    BEGIN
      -- a connection that ended by itself in between is not a failure
      DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;
      SET @wwdctl_sql = CONCAT('KILL CONNECTION ', v_id);
      PREPARE wwdctl_s FROM @wwdctl_sql;
      EXECUTE wwdctl_s;
      DEALLOCATE PREPARE wwdctl_s;
    END;
  END LOOP;
  CLOSE c;
  SELECT COUNT(*) AS remaining FROM information_schema.processlist WHERE user = p_user;
END$$

DROP PROCEDURE IF EXISTS wwdctl.wwd_drop_user$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_drop_user(IN p_user VARCHAR(32), IN p_host VARCHAR(64))
  SQL SECURITY DEFINER
BEGIN
  IF p_user IS NULL OR CHAR_LENGTH(p_user) <> 31 OR NOT REGEXP_LIKE(p_user, '\\Awwdu_[0-9a-f]{26}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_drop_user: not a wwd user name';
  END IF;
  IF p_host IS NULL OR CHAR_LENGTH(p_host) > 64 OR NOT REGEXP_LIKE(p_host, '\\A[A-Za-z0-9_.:-]{1,64}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_drop_user: not a plain host';
  END IF;
  SET @wwdctl_sql = CONCAT('DROP USER IF EXISTS ''', p_user, '''@''', p_host, '''');
  PREPARE wwdctl_s FROM @wwdctl_sql;
  EXECUTE wwdctl_s;
  DEALLOCATE PREPARE wwdctl_s;
END$$

DROP PROCEDURE IF EXISTS wwdctl.wwd_drop_db$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_drop_db(IN p_db VARCHAR(64))
  SQL SECURITY DEFINER
BEGIN
  IF p_db IS NULL OR CHAR_LENGTH(p_db) <> 36 OR NOT REGEXP_LIKE(p_db, '\\Awwd_[0-9a-f]{32}\\z', 'c') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'wwd_drop_db: not a wwd database name';
  END IF;
  SET @wwdctl_sql = CONCAT('DROP DATABASE IF EXISTS `', p_db, '`');
  PREPARE wwdctl_s FROM @wwdctl_sql;
  EXECUTE wwdctl_s;
  DEALLOCATE PREPARE wwdctl_s;
END$$

-- The leftovers a Sweep looks at: only names of exactly our shape are ever listed.
DROP PROCEDURE IF EXISTS wwdctl.wwd_list_dbs$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_list_dbs()
  SQL SECURITY DEFINER
BEGIN
  SELECT schema_name FROM information_schema.schemata WHERE REGEXP_LIKE(schema_name, '\\Awwd_[0-9a-f]{32}\\z', 'c') ORDER BY schema_name;
END$$

DROP PROCEDURE IF EXISTS wwdctl.wwd_list_users$$
CREATE DEFINER=`root`@`localhost` PROCEDURE wwdctl.wwd_list_users()
  SQL SECURITY DEFINER
BEGIN
  SELECT user, host FROM mysql.user WHERE REGEXP_LIKE(user, '\\Awwdu_[0-9a-f]{26}\\z', 'c') ORDER BY user, host;
END$$

DELIMITER ;
