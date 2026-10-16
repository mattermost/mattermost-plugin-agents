// Posts the eval report produced by `evalviewer report` on the pull request.
// The comment exists only while there is something actionable: it is created
// or updated when the report lists regressions, and an earlier comment is
// marked resolved once they are gone. Passing runs never add a comment.

const fs = require('fs');

// Matches the heading written by evalviewer, including comments from earlier
// versions of this workflow.
const MARKER = '## 🤖 LLM Evaluation Results';
const RESOLVED = '✅ No eval regressions';

async function postEvalComment(github, context, commentPath) {
    const body = fs.existsSync(commentPath) ? fs.readFileSync(commentPath, 'utf8').trim() : '';
    const repo = {owner: context.repo.owner, repo: context.repo.repo};

    const comments = await github.paginate(github.rest.issues.listComments, {
        ...repo,
        issue_number: context.issue.number,
        per_page: 100,
    });
    const existing = comments.find((comment) => comment.user.type === 'Bot' && comment.body.includes(MARKER));

    if (body) {
        if (existing) {
            await github.rest.issues.updateComment({...repo, comment_id: existing.id, body});
            console.log(`Updated eval comment ${existing.id}`);
        } else {
            const {data} = await github.rest.issues.createComment({...repo, issue_number: context.issue.number, body});
            console.log(`Created eval comment ${data.id}`);
        }
        return;
    }

    if (existing && !existing.body.includes(RESOLVED)) {
        const sha = context.payload.pull_request.head.sha.slice(0, 7);
        await github.rest.issues.updateComment({
            ...repo,
            comment_id: existing.id,
            body: `${MARKER}\n\n${RESOLVED} as of ${sha}.\n`,
        });
        console.log(`Marked eval comment ${existing.id} resolved`);
        return;
    }

    console.log('Nothing actionable; no eval comment posted.');
}

module.exports = {postEvalComment};
