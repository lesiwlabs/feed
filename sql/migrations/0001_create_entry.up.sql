create table group_entry (
    url text primary key,
    "group" text not null,
    subject text,
    body text,
    date timestamptz not null
);

create index idx_entry_date ON group_entry(date);
create index idx_entry_group ON group_entry("group");
