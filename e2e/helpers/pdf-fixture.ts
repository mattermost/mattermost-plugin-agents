// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * Builds a minimal one-page PDF with a correct cross-reference table. Each
 * line is drawn as its own text line in Helvetica; no lines gives a page
 * with no text, like a scanned image-only PDF. Lines must be printable ASCII
 * without parentheses or backslashes.
 */
export function buildPDF(lines: string[]): Buffer {
    const content = lines.length === 0 ? '' : [
        'BT /F1 12 Tf 72 720 Td 14 TL',
        ...lines.map((line, i) => `${i === 0 ? '' : 'T* '}(${line}) Tj`),
        'ET',
    ].join('\n');

    const objects = [
        '<< /Type /Catalog /Pages 2 0 R >>',
        '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
        '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
        `<< /Length ${Buffer.byteLength(content, 'latin1')} >>\nstream\n${content}\nendstream`,
        '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>',
    ];

    let pdf = '%PDF-1.4\n';
    const offsets: number[] = [];
    objects.forEach((obj, i) => {
        offsets.push(Buffer.byteLength(pdf, 'latin1'));
        pdf += `${i + 1} 0 obj\n${obj}\nendobj\n`;
    });
    const xref = Buffer.byteLength(pdf, 'latin1');
    pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
    for (const offset of offsets) {
        pdf += `${String(offset).padStart(10, '0')} 00000 n \n`;
    }
    pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
    return Buffer.from(pdf, 'latin1');
}
