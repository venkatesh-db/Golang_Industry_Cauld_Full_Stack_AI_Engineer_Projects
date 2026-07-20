-- RailCache seed data. Idempotent-ish: truncate then insert.
TRUNCATE seat_availability, train_stops, trains, stations RESTART IDENTITY CASCADE;

INSERT INTO stations (code, name) VALUES
    ('NDLS','New Delhi'), ('BCT','Mumbai Central'), ('HWH','Howrah'),
    ('MAS','Chennai Central'), ('SBC','Bengaluru'), ('ADI','Ahmedabad'),
    ('JP','Jaipur'), ('LKO','Lucknow'), ('PNBE','Patna'), ('KOTA','Kota');

INSERT INTO trains (id, number, name, source_code, dest_code) VALUES
    (1,'12951','Mumbai Rajdhani',        'NDLS','BCT'),
    (2,'12953','August Kranti Rajdhani', 'NDLS','BCT'),
    (3,'12909','Garib Rath',             'NDLS','BCT'),
    (4,'12302','Howrah Rajdhani',        'NDLS','HWH'),
    (5,'12621','Tamil Nadu Express',     'NDLS','MAS'),
    (6,'12627','Karnataka Express',      'NDLS','SBC'),
    (7,'12958','ADI SJ Rajdhani',        'NDLS','ADI'),
    (8,'12004','Lucknow Shatabdi',       'NDLS','LKO');

-- Stops (ordered). NDLS->BCT hot route via JP/KOTA/ADI for trains 1,2,3.
INSERT INTO train_stops (train_id, station_code, seq, arr, dep, day_offset) VALUES
    (1,'NDLS',1,NULL,'16:25',0),(1,'KOTA',2,'21:40','21:45',0),(1,'ADI',3,'02:40','02:45',1),(1,'BCT',4,'08:15',NULL,1),
    (2,'NDLS',1,NULL,'17:40',0),(2,'KOTA',2,'22:55','23:00',0),(2,'BCT',3,'10:55',NULL,1),
    (3,'NDLS',1,NULL,'15:10',0),(3,'JP',2,'19:30','19:35',0),(3,'ADI',3,'03:10','03:15',1),(3,'BCT',4,'09:05',NULL,1),
    (4,'NDLS',1,NULL,'16:50',0),(4,'PNBE',2,'23:55','00:05',1),(4,'HWH',3,'09:55',NULL,1),
    (5,'NDLS',1,NULL,'22:30',0),(5,'KOTA',2,'04:10','04:15',1),(5,'MAS',3,'07:00',NULL,2),
    (6,'NDLS',1,NULL,'20:15',0),(6,'JP',2,'01:05','01:10',1),(6,'SBC',3,'11:40',NULL,2),
    (7,'NDLS',1,NULL,'19:55',0),(7,'JP',2,'00:30','00:35',1),(7,'ADI',3,'09:30',NULL,1),
    (8,'NDLS',1,NULL,'06:10',0),(8,'LKO',2,'12:35',NULL,0);

-- Availability for a rolling two-week window starting today, so the seeded data
-- never drifts out of the app's validation window (today .. +N days).
INSERT INTO seat_availability (train_id, travel_date, class, total, available, updated_at)
SELECT t.id,
       d::date,
       c.class,
       c.total,
       -- deterministic pseudo-availability so demos are reproducible
       GREATEST(0, c.total - ((t.id * 7 + EXTRACT(DAY FROM d)::int * 3) % (c.total + 1))),
       now()
FROM trains t
CROSS JOIN generate_series(CURRENT_DATE, CURRENT_DATE + INTERVAL '13 days', INTERVAL '1 day') d
CROSS JOIN (VALUES ('SL',72), ('3A',48), ('2A',24)) AS c(class, total);
