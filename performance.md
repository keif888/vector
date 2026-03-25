# Performance Test 1

Initial attempt with ClusteredAt *time.Time to store when a column was clustered (null if not clustered).  
This attempt was stopped at 5k when it became obvious that Qdrant can not null a column out, it removes the json entry from the payload instead.  
This makes the query unable to detect that the value is null, so it fails.  
The reason that ClusteredAt was rejected was because the json.Unmarshall command will include a Null for ClusteredAt when generating the payload field.  

Please note that MatchedAt *time.Time will suffer the same issue in Qdrant!  

## Clusterings

MariaDB Vector -   5k = 18.5s (no order by or limit)  
MariaDB Vector -   5k = 55.0s (order by and limit 15)  
MariaDB Go     -   5k = 4.8s  

Postgres Vector -   5k = 1m 48s (order by and limit 5)  
Postgres Vector -   5k = 14.4s (order by and limit 10)  
Postgres Vector -   5k = 32.6s (order by and limit 15)  
Postgres Vector -   5k = 1m 33s (order by and limit 100)  
Postgres Vector -   5k = 1m 12s (no order by or limit)  
Postgres Go     -   5k = 5.5s  


SQLite Vector -   5k = 2m 28.6s (order by and limit 15)  
SQLite Vector -   5k = 1m 48.5s (no order by or limit)  
SQLite Go     -   5k = 4.7s  


Qdrant Vector -   5k = 1m 38s  


# Performance Test 2

This test uses Clustered bool to store whether a marker has been clustered (successfully or otherwise).  

## Clusterings

### Expected results
These are the results from the set of random files that I generated.  

5k expected results:  
4953 unclustered faces  
153 new clusters  

25k expected results:  
24744 unclustered faces  
850 new clusters  

100k expected results:  
99077 unclustered faces  
3321 new clusters  

### Performance Stats 5k

MariaDB Load   -   5k = 5.0s  
MariaDB Vector -   5k = 15.9s  
MariaDB Go     -   5k = 4.8s  

Postgres Load   -   5k = 7.0s  
Postgres Vector -   5k = 5.8s, wrong number of clusters (152).  Reran and it found 1 cluster.  
Postgres Vector -   5k = 40.7s, correct cluster count, after reset.  
Postgres Vector -   5k = 18.6s, wrong number of clusters (152), 16 MarkerUID's didn't find matches.  
Postgres Vector -   5k = 45.6s, correct cluster count, using CTE and SET hnsw.ef_search = 120; SET hnsw.iterative_scan = strict_order;  
Postgres Vector -   5k = 58.1s, incorrect cluster count, using CTE and SET hnsw.iterative_scan = strict_order;  
Postgres Vector -   5k = 14.2s, correct cluster count (but 4 markers didn't find anything), using CTE and SET hnsw.iterative_scan = relaxed_order;  
Postgres Vector -   5k = 30.4s, correct cluster count (but 1 maker didn't find anything), using CTE and SET hnsw.ef_search = 120; SET hnsw.iterative_scan = relaxed_order;  
Change index from m=8 to m=16.  (ef_construction defaults to 64)  
Postgres Load   -   5k = 13.7s
Postgres Vector -   5k = 17.9s, correct cluster count, using CTE and SET hnsw.ef_search = 120; SET hnsw.iterative_scan = strict_order;  

Change index from m=16 to m=16, ef_construction=100.  
Postgres Load   -   5k = 15.2s
Postgres Vector -   5k = 12.6s, correct cluster count, using CTE and SET hnsw.ef_search = 120; SET hnsw.iterative_scan = strict_order;  


Postgres Go     -   5k = 4.7s  

SQLite Load   -   5k = 4.5s  
SQLite Vector -   5k = 1m 45.5s  
SQLite Go     -   5k = 4.7s  

Qdrant Load   -   5k = 2.2s  
Qdrant Vector -   5k = 1m 10.2s  

### Performance Stats 25k

MariaDB Load   -  25k = 1m 10.3s  
MariaDB Vector -  25k = 6m 6.1s 850 clusters Very low CPU usage, peak less than 50%, average 20%.  
MariaDB Go     -  25k = 2m 47.7s 24744/850 clusters.  

Postgres Load   -  25k = 38.7s  
SET hnsw.ef_search = 120;SET hnsw.iterative_scan = relaxed_order;  
Postgres Vector -  25k = 59.2s 841 clusters. 294 markers failed to find records.  2nd pass took 1s, and found 1 new cluster for 237 matches.  End result fail.  
Postgres Vector -  25k = 1m 11.1s 842 clusters. 302 markers failed to find records.  2nd pass took 1.1s, and found 0 new clusters for 242 matches.  End result fail.  

SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order;  
Postgres Vector -  25k = 1m 7.0s 840 clusters.  469 markers failed to find records.  2nd and 3rd passes (~11s) failed to find the markers.  End result fail.  
Revert to non CTE based query (same base as MariaDB and SQLite) with Postgres specific order by and limit.  
Postgres Vector -  25k = 1m 34.0s 795 clusters.  3167 markers failed to find records.  2nd pass 5.4s failed to find the markers.  End result fail.  

Change index from m=8 to m=16, keep SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order;  
Postgres Load   -  25k = 1m 19.4s
Postgres Vector -  25k = 1m 9.9s 850 clusters. 6 markers failed to find records.  2nd pass 55ms found no new clusters.  End result fail.  

Change index from m=16 to "m=16, ef_construction=100", keep SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order;  
Postgres Load   -  25k = 1m 34.1s
Postgres Vector -  25k = 1m 10.9s 850 clusters. End result pass.  

Postgres Go     -  25k = 1m 42.4s  

SQLite Load   -  25k = 13.8s  
SQLite Vector -  25k = 54m 24.5s 850 clusters.  Up to 100% cpu usage on a core when running, visually averaging 25% across 6 cores.  
SQLite Go     -  25k = 3m 48.9s 24744/850 clusters.  Up to 100% cpu usage on a core when running, visually averaging 50% across 6 cores.  

Qdrant Load   -  25k =  10.8s  

Limit set to 2,000,000   
Qdrant Vector -  25k =  25m 54.1s 850 clusters.  Up to 60% cpu usage on a core when running, visually averaging 30% across 6 cores.  

Limit set to 15  
Qdrant Vector -  25k =  19m 22.0s 850 clusters.  Up to 100% cpu usage on a core when running, visually averaging 50% across 6 cores.  

Add missing indexes (Clustered and others) and Limit still set to 15.  
Qdrant Load   -  25k =  10.9s  
Qdrant Vector -  25k =  12m 24.1s 850 clusters.  Top showing qdrant 2.8 to 2.95 across 6 cores.  

As above, plus FullScanThreshold = 0 in index config.  
Qdrant Load   -  25k =  10.7s  
Qdrant Vector -  25k =  13m 5.1s 850 clusters.  Top showing qdrant 2.8 to 2.95 across 6 cores.  


### Performance Stats 100k

MariaDB Load   - 100k =  4m 28.3s  
MariaDB Vector - 100k =  Stopped at 3h, 7391 of 99077 markers processed, 2315 clusters found.  Top showing mariadb 0.1 to 0.2 across 6 cores.  
3s per match on average, so although there is "only" ~1000 clusters to find, it's still got to wade through the ones that don't cluster.  And that's going to take another 76 hours to complete.  
Restarted with 91686, and 15m status logging implemented.  
MariaDB IO from SHOW ENGINE INNODB STATUS;  
3617.62 reads/s, 16295 avg bytes/read, 2.08 writes/s, 1.76 fsyncs/s  
Ran for 15m, stopped, and resized MariaDB bufferpool.  Processed 632 markers in 15 minutes.  
Restarted with 91054, and it's performing MUCH better, no longer IO bound...  Top showing mariadb 0.9 to 1.0 across 6 cores.  
Rate is now ~20,000 markers per 15 minutes, which equates to < 1.5h to process 100k.  

2nd Full Run with adjusted memory size:
MariaDB Vector - 100k = 1h 19m 3.9s 99077/3321 clusters.  Top showing mariadb 0.8 to 1.0 across 6 cores.  

MariaDB Go     - 100k =  32m 22.3s 99077/3321 clusters.  Top showing vector 2.8 to 2.95 across 6 cores.  


Postgres Load   - 100k =  14m 58.9s
Postgres Vector - 100k =  20m 2.2s 99077/3315 clusters.  (Top wasn't running)
Using m=16, ef_construction=100 and SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order; still results in lots (1079) of "no record found", which indicates that the index isn't working again.  Having to retune for different numbers of records makes this not a feasible solution.

Postgres Go     - 100k =  

Not run as expected to take WAY to long  
SQLite Load   - 100k =  
SQLite Vector - 100k =  
SQLite Go     - 100k =  

Qdrant Load   - 100k =  1m 4.4s  +1m for optimisations to complete (indexing)  
Qdrant Vector - 100k =  34m 26.3s 99077/3310 clusters.  Top showing qdrant 0.9 to 5.79 across 6 cores.  
9506 "no record found" were returned.  

Reload with ef_construction = 200.  
Qdrant Load   - 100k =  47.5s  +1m for optimisations to complete (indexing)  
Qdrant Vector - 100k =  52m 25.3s 99077/3319 clusters.  Top showing qdrant 0.9 to 5.79 across 6 cores.  
1168 "no record found" were returned.  
Running some of the "no record found" individually was able to find them.

Patch with ef_construction = 260.  
Qdrant Vector - 100k =   99077/3319 clusters.  Top showing qdrant 0.9 to 5.79 across 6 cores.  
2072 "no record found" were returned.  

# Clustering V1
The clustering v1 uses a single pass search against the vector database looking for embeddings which are less than the clustering distance.  Any marker that is matched is then excluded from further analysis.  
Mariadb index configuration = "m=8"  
Postgres index configuration = "m=16, ef_construction=100"  
Postgres query configuration = "limit=15, hnsw.ef_search = 120, hnsw.iterative_scan = strict_order"  
Qdrant index configuration = "m=16, ef_construction=100, full_scan_threshold=10000"  
Qdrant query configuration = "limit=15"  
Sqlite does not support indexes  
Sqlite query configuration = "k=1024"  

## 5k clustering

MariaDB Load   -   5k = 5.0s  
MariaDB Vector -   5k = 15.9s  
MariaDB Go     -   5k = 4.8s  

Postgres Load   -   5k = 15.2s
Postgres Vector -   5k = 12.6s
Postgres Go     -   5k = 4.7s  

SQLite Load   -   5k = 4.5s  
SQLite Vector -   5k = 1m 45.5s  
SQLite Go     -   5k = 4.7s  

Qdrant Load   -   5k = 2.2s  
Qdrant Vector -   5k = 1m 10.2s  

## 25k clustering

MariaDB Load   -  25k = 1m 10.3s  
MariaDB Vector -  25k = 6m 6.1s 24744/850 clusters  
MariaDB Go     -  25k = 2m 47.7s 24744/850 clusters.  

Change index from m=16 to "m=16, ef_construction=100", keep SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order;  
Postgres Load   -  25k = 1m 34.1s
Postgres Vector -  25k = 1m 10.9s 850 clusters. End result pass.  
Postgres Go     -  25k = 1m 42.4s  

SQLite Load   -  25k = 13.8s  
SQLite Vector -  25k = 54m 24.5s 850 clusters.  Up to 100% cpu usage on a core when running, visually averaging 25% across 6 cores.  
SQLite Go     -  25k = 3m 48.9s 24744/850 clusters.  Up to 100% cpu usage on a core when running, visually averaging 50% across 6 cores.  

Qdrant Load   -  25k =  10.9s  
Qdrant Vector -  25k =  12m 24.1s 850 clusters.  Top showing qdrant 2.8 to 2.95 across 6 cores.  

## 100k clustering
MariaDB Load   - 100k =  4m 28.3s  
MariaDB Vector - 100k = 1h 19m 3.9s 99077/3321 clusters.  Top showing mariadb 0.8 to 1.0 across 6 cores.  
MariaDB Go     - 100k =  32m 22.3s 99077/3321 clusters.  Top showing vector 2.8 to 2.95 across 6 cores.  

Postgres Load   - 100k =  14m 58.9s
Postgres Vector - 100k =  20m 2.2s 99077/3315 clusters.  (top wasn't running) This is a FAIL!  
Postgres Go     - 100k =  

Not run as expected to take WAY to long  
SQLite Load   - 100k =  
SQLite Vector - 100k =  
SQLite Go     - 100k =  

Qdrant Load   - 100k =  1m 4.4s  +1m for optimisations to complete (indexing)  
Qdrant Vector - 100k =  34m 26.3s 99077/3310 clusters.  Top showing qdrant 0.9 to 5.79 across 6 cores.  This is a FAIL!  



# Clustering V2
The clustering v2 is an implementation of the clustering algorithm used in PhotoPrism for the dbms'.  
That means that it will keep looking for adjoining clusters until it can't find anymore.  
Mariadb index configuration = "m=8"  
Postgres index configuration = "m=16, ef_construction=320"  
Postgres query configuration = "limit=15, hnsw.ef_search = 120, hnsw.iterative_scan = strict_order"  
Qdrant index configuration = "m=16, ef_construction=320, full_scan_threshold=10000"  
Qdrant query configuration = "limit=15"  
Sqlite does not support indexes  
Sqlite query configuration = "k=15"  

## 5k clustering

MariaDB Load   -  5k = 9.2s  
MariaDB Vector -  5k = 56.7s  
MariaDB Go     -  5k = 12.7s  

Postgres Load   - 5k = 37.3s  
Postgres Vector - 5k = 42.8s  
Postgres Go     - 5k = 12.2s  


SQLite Load   -   5k = 3.3s  
SQLite Vector -   5k = 2m 52.3s  
SQLite Go     -   5k = 14.6s  

Qdrant Load   -   5k = 2.9s  
Qdrant Vector -   5k = 1m 31.7s  


## 25k clustering

MariaDB Load    - 25k = 1m 9.8s  
MariaDB Vector  - 25k = 12m 43.8s 24744/850. Top showing mariadb 0.8 to 1.0 across 6 cores.  
MariaDB Go      - 25k = 3m 15.3s 24744/850. Top showing vector 2.8 to 2.95 across 6 cores.  

Postgres Load   - 25k = 2m 32.3s   
Postgres Vector - 25k = 1m 50.0s 24744/850. Top showing postgres 0.8 to 1.0 across 6 cores.  

SQLite Load     - 25k = 13.3s  
SQLite Vector   - 25k = 49m 45.3s 24744/850.  Top showing vector 0.9 to 1.0 across 6 cores.  

Qdrant Load     - 25k = 10.7s  
Qdrant Vector   - 25k = 27m 50.6s 24744/850. Top showing qdrant 2.8 to 2.95 across 6 cores.  

## 100k clustering

MariaDB Load    - 100k = 4m 37.7s  
MariaDB Vector  - 100k = 2h 39m 38.5s 99077/3321. Top showing mariadb 0.8 to 1.0 across 6 cores.  
MariaDB Go      - 100k = 34m 18.5s 99077/3321. Top showing vector 2.8 to 2.95 across 6 cores.  

Postgres Load   - 100k = 18m 8.8s  
Postgres Vector - 100k = 14m 47.8s 99077/3321. Top showing postgres 0.8 to 1.0 across 6 cores.  
Please note that there were 56 cases where postgres failed to find a match.  Every request should match at least 1 record.  

SQLite Load     - 100k =   
SQLite Vector   - 100k = .  Top showing vector 0.9 to 1.0 across 6 cores.  

Qdrant Load     - 100k = 45.2s, +1m for optimisations to complete (indexing)  
Qdrant Vector   - 100k = 2h 23m 21.6s 99077/3320. Top showing qdrant 0.8 to 2.95 across 6 cores.  
Please note that there were 2040 cases where Qdrant failed to find a match.  Every request should match at least 1 record.  

## 100k clustering 2nd attempt
Postgres index configuration = "m=24, ef_construction=320"  
Qdrant index configuration = "m=24, ef_construction=320, full_scan_threshold=10000"  

Postgres Load   - 100k = 30m 52.9s  
Postgres Vector - 100k = 18m 30.3s 99077/3321. Top showing postgres 0.8 to 1.0 across 6 cores.  

Qdrant Load     - 100k = 54.5s, +1m for optimisations to complete (indexing)  
Qdrant Vector   - 100k = 3h 49m 28.5s 99077/3320. Top showing qdrant 0.8 to 2.95 across 6 cores.  
Please note that there were 429 cases where Qdrant failed to find a match.  Every request should match at least 1 record.  