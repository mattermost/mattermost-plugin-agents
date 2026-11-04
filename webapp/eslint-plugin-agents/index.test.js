// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/** @jest-environment node */
/* eslint-env jest */

const {RuleTester} = require('eslint');

const plugin = require('.');

const ruleTester = new RuleTester({
    parser: require.resolve('@typescript-eslint/parser'),
    parserOptions: {ecmaVersion: 'latest', sourceType: 'module', ecmaFeatures: {jsx: true}},
    settings: {'jsx-a11y': {polymorphicPropName: 'as'}},
});

const clickError = {message: 'Visible, non-interactive elements with click handlers must have at least one keyboard listener.'};

ruleTester.run('click-events-have-key-events', plugin.rules['click-events-have-key-events'], {
    valid: [
        'const Row = styled.button``; const x = <Row onClick={open}/>;',
        'const Row = styled.div``; const x = <Row onClick={open} onKeyDown={onKey}/>;',
        'const x = <Imported onClick={open}/>;',
        'const Row = styled.div``; const x = <Row as="button" onClick={open}/>;',
    ],
    invalid: [
        {code: 'const x = <div onClick={open}/>;', errors: [clickError]},
        {code: 'const Row = styled.div``; const x = <Row onClick={open}/>;', errors: [clickError]},
        {code: 'const Row = styled.div<{$open: boolean}>``; const x = <Row onClick={open}/>;', errors: [clickError]},
        {code: 'const Row = styled.div.attrs({role: "row"})``; const x = <Row onClick={open}/>;', errors: [clickError]},
        {code: 'const Row = styled("span")``; const x = <Row onClick={open}/>;', errors: [clickError]},
        {code: 'const Base = styled.div``; const Row = styled(Base)``; const x = <Row onClick={open}/>;', errors: [clickError]},
        {code: 'const Row = styled.button``; const x = <Row as="div" onClick={open}/>;', errors: [clickError]},
    ],
});

ruleTester.run('no-static-element-interactions', plugin.rules['no-static-element-interactions'], {
    valid: [
        'const Row = styled.div``; const x = <Row role="button" tabIndex={0} onClick={open} onKeyDown={onKey}/>;',
        'const Tabs = styled.div``; const x = <Tabs role="tablist" onKeyDown={onKey}/>;',
    ],
    invalid: [
        {code: 'const Header = styled.div``; const x = <Header onClick={toggle}/>;', errors: 1},
    ],
});
