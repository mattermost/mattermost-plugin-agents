// Keeps one eval comment on a pull request. `evalviewer report` decides what it
// says: it is given the existing comment (saveEvalComment) and writes the new
// body, or nothing when the existing comment should stay as it is.

const fs = require('fs');

// Matches the heading written by evalviewer, including comments from earlier
// versions of this workflow.
const MARKER = '## 🤖 LLM Evaluation Results';

async function findEvalComment(github, context) {
    const comments = await github.paginate(github.rest.issues.listComments, {
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: context.issue.number,
        per_page: 100,
    });
    // Only the workflow's own comments: another bot may quote the marker.
    return comments.find((comment) => comment.user?.login === 'github-actions[bot]' && (comment.body || '').includes(MARKER));
}

async function saveEvalComment(github, context, path) {
    const existing = await findEvalComment(github, context);
    fs.writeFileSync(path, existing ? existing.body : '');
}

async function postEvalComment(github, context, commentPath) {
    const body = fs.existsSync(commentPath) ? fs.readFileSync(commentPath, 'utf8').trim() : '';
    if (!body) {
        console.log('Leaving the eval comment as it is.');
        return;
    }

    const repo = {owner: context.repo.owner, repo: context.repo.repo};
    const existing = await findEvalComment(github, context);
    if (existing) {
        await github.rest.issues.updateComment({...repo, comment_id: existing.id, body});
        console.log(`Updated eval comment ${existing.id}`);
        return;
    }
    const {data} = await github.rest.issues.createComment({...repo, issue_number: context.issue.number, body});
    console.log(`Created eval comment ${data.id}`);
}

module.exports = {saveEvalComment, postEvalComment};
