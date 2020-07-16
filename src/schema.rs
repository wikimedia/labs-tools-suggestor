table! {
    edits (id) {
        id -> Unsigned<Integer>,
        wiki -> Varchar,
        text -> Blob,
        summary -> Varchar,
        baserevid -> Unsigned<Integer>,
        pageid -> Unsigned<Integer>,
        pagename -> Varchar,
        state -> Varchar,
    }
}
