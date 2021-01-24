use super::models;
use anyhow::{Error, Result};
use mediawiki::api::Api;

const USER_AGENT: &str = toolforge::user_agent!("suggestor");

async fn build_api(wiki: &str, token: Option<&str>) -> Api {
    let mut api = Api::new(&format!("https://{}/w/api.php", wiki))
        .await
        .unwrap();
    api.set_user_agent(USER_AGENT);
    // This is an interactive tool, disable maxlag
    api.set_maxlag(None);
    if let Some(token) = token {
        api.set_oauth2(token);
    }
    api
}

pub async fn get_username(token: &str) -> Option<String> {
    let api = build_api("meta.wikimedia.org", Some(token)).await;

    match api
        .get_query_api_json(&mediawiki::hashmap![
               "action".to_string() => "query".to_string(),
               "meta".to_string() => "userinfo".to_string(),
               "formatversion".to_string() => "2".to_string()
        ])
        .await
    {
        Ok(resp) => {
            let info = &resp["query"]["userinfo"];
            if info.get("anon").is_some() {
                // Logged out
                return None;
            }
            Some(
                resp["query"]["userinfo"]["name"]
                    .as_str()
                    .unwrap()
                    .to_string(),
            )
        }
        // token expired or something
        Err(_) => None,
    }
}

pub async fn get_diff(edit: &models::Edit) -> String {
    let api = build_api(&edit.wiki, None).await;
    let params = api.params_into(&[
        ("action", "compare"),
        ("formatversion", "2"),
        ("fromrev", &edit.baserevid.to_string()),
        ("toslots", "main"),
        ("totext-main", std::str::from_utf8(&edit.text).unwrap()),
    ]);
    match api.post_query_api_json(&params).await {
        Ok(resp) => resp["compare"]["body"].as_str().unwrap().to_string(),
        // TODO: error handling
        Err(_) => panic!("API action=compare request failed"),
    }
}

pub async fn make_edit(edit: models::Edit, token: &str) -> Result<()> {
    let mut api = build_api(&edit.wiki, Some(token)).await;
    let token = api.get_edit_token().await?;
    if token == "+\\" {
        // We got logged out :(
        return Err(Error::msg(
            "OAuth token no longer valid, please log in again",
        ));
    }
    let params = api.params_into(&[
        ("action", "edit"),
        ("formatversion", "2"),
        ("assert", "user"),
        ("pageid", &edit.pageid.to_string()),
        ("text", std::str::from_utf8(&edit.text).unwrap()),
        // TODO: we should allow customizing the summary on acceptance?
        (
            "summary",
            &format!("Edit on behalf of Tor user: {}", edit.summary),
        ),
        ("baserevid", &edit.baserevid.to_string()),
        ("token", &token),
    ]);
    // FIXME check for an API error here...
    api.post_query_api_json(&params).await?;
    Ok(())
}
