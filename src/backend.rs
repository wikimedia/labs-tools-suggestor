use super::models::{Edit, NewEdit};
use super::schema;
use super::web::{EditForm, ToolsDb};
use anyhow::Result;
use diesel::prelude::*;

/// Load an edit from the db
pub async fn load_edit(edit_id: u32, conn: &ToolsDb) -> Option<Edit> {
    use schema::edits::dsl::*;
    conn.run(move |c| {
        let edit = edits.filter(id.eq(edit_id)).first(c);
        match edit {
            Ok(edit) => Some(edit),
            Err(_) => None,
        }
    })
    .await
}

/// Update an edit to a new state
pub async fn set_state(
    edit_id: u32,
    new_state: String,
    conn: &ToolsDb,
) -> Result<()> {
    use schema::edits::dsl::*;
    conn.run(move |c| {
        diesel::update(edits.filter(id.eq(edit_id)))
            .set(state.eq(&new_state))
            .execute(c)?;
        Ok(())
    })
    .await
}

/// Get a vec of edits with the specified state
pub async fn load_edits_with_state(
    edit_state: String,
    conn: &ToolsDb,
) -> Result<Vec<Edit>> {
    use schema::edits::dsl::*;
    conn.run(move |c| {
        Ok(edits
            .filter(state.eq(&edit_state))
            .order(id.desc())
            .load(c)?)
    })
    .await
}

/// Save a new edit into the db
pub async fn insert_edit(form: EditForm, conn: &ToolsDb) -> Result<Edit> {
    conn.run(move |c| {
        let new_edit = NewEdit {
            wiki: &form.wiki,
            text: form.text.as_bytes(),
            summary: &form.summary,
            baserevid: &form.baserevid,
            pageid: &form.pageid,
            pagename: &form.pagename,
            state: "pending",
        };
        // FIXME: validate data input is sane
        match diesel::insert_into(schema::edits::table)
            .values(&new_edit)
            .execute(c)
        {
            Ok(_) => {
                use schema::edits::dsl::*;
                Ok(edits.order(id.desc()).first(c).unwrap())
            }
            Err(e) => Err(e.into()),
        }
    })
    .await
}
