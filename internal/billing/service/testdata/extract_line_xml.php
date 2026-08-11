<?php
$path = $argv[1] ?? '';
if ($path === '' || !is_file($path)) {
    fwrite(STDERR, "usage: php extract_line_xml.php <file.xml>\n");
    exit(1);
}
$d = new DOMDocument();
$d->load($path);
$xp = new DOMXPath($d);
$xp->registerNamespace('cac', 'urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2');
$xp->registerNamespace('cbc', 'urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2');
$id = $xp->query('//cbc:ID[parent::Invoice or parent::*[local-name()="Invoice"]]')->item(0);
if (!$id) {
    $id = $xp->query('//cbc:ID')->item(0);
}
echo "DOCUMENT_ID: " . ($id ? $id->textContent : '?') . PHP_EOL;
foreach ($xp->query('//cac:InvoiceLine') as $i => $line) {
    $n = $i + 1;
    echo PHP_EOL . "=== LINE $n ===" . PHP_EOL;
    $qty = $xp->query('.//cbc:InvoicedQuantity', $line)->item(0);
    echo "InvoicedQuantity: " . ($qty ? $qty->textContent : '') . PHP_EOL;
    $lea = $xp->query('./cbc:LineExtensionAmount', $line)->item(0);
    echo "LineExtensionAmount: " . ($lea ? $lea->textContent : '') . PHP_EOL;
    $alt = $xp->query('.//cac:AlternativeConditionPrice/cbc:PriceAmount', $line)->item(0);
    $ptc = $xp->query('.//cac:AlternativeConditionPrice/cbc:PriceTypeCode', $line)->item(0);
    echo "AltPriceAmount: " . ($alt ? $alt->textContent : '') . " type=" . ($ptc ? $ptc->textContent : '') . PHP_EOL;
    $price = $xp->query('.//cac:Price/cbc:PriceAmount', $line)->item(0);
    echo "PriceAmount: " . ($price ? $price->textContent : '') . PHP_EOL;
    $tt = $xp->query('./cac:TaxTotal/cbc:TaxAmount', $line)->item(0);
    echo "TaxTotal/TaxAmount: " . ($tt ? $tt->textContent : '') . PHP_EOL;
    $taxable = $xp->query('.//cac:TaxSubtotal/cbc:TaxableAmount', $line)->item(0);
    $taxAmt = $xp->query('.//cac:TaxSubtotal/cbc:TaxAmount', $line)->item(0);
    $code = $xp->query('.//cbc:TaxExemptionReasonCode', $line)->item(0);
    $scheme = $xp->query('.//cac:TaxScheme/cbc:ID', $line)->item(0);
    echo "TaxSubtotal: taxable=" . ($taxable ? $taxable->textContent : '') . " tax=" . ($taxAmt ? $taxAmt->textContent : '') . PHP_EOL;
    echo "TaxExemptionReasonCode: " . ($code ? $code->textContent : '') . " scheme=" . ($scheme ? $scheme->textContent : '') . PHP_EOL;
    $desc = $xp->query('.//cbc:Description', $line)->item(0);
    echo "Description: " . ($desc ? trim($desc->textContent) : '') . PHP_EOL;
}
