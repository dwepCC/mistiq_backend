<?php
/**
 * Renderiza XML UBL 2.1 desde JSON Greenter (stdin → stdout).
 * Construye el modelo manualmente (sin JMS) para pruebas de integración.
 */
declare(strict_types=1);

$autoload = dirname(__DIR__, 5) . '/facturador_lycet/vendor/autoload.php';
if (!is_file($autoload)) {
    fwrite(STDERR, "autoload not found: {$autoload}\n");
    exit(1);
}
require $autoload;

use Greenter\Model\Client\Client;
use Greenter\Model\Company\Address;
use Greenter\Model\Company\Company;
use Greenter\Model\Sale\Invoice;
use Greenter\Model\Sale\Prepayment;
use Greenter\Model\Sale\SaleDetail;
use Greenter\Xml\Builder\InvoiceBuilder;

$json = stream_get_contents(STDIN);
if ($json === false || trim($json) === '') {
    fwrite(STDERR, "empty stdin\n");
    exit(1);
}

/** @var array<string,mixed> $p */
$p = json_decode($json, true);
if (!is_array($p)) {
    fwrite(STDERR, "invalid json\n");
    exit(1);
}

$companyAddr = new Address();
$companyAddr->setUbigueo((string) ($p['company']['address']['ubigueo'] ?? ''));
$companyAddr->setCodigoPais((string) ($p['company']['address']['codigoPais'] ?? 'PE'));
$companyAddr->setDireccion((string) ($p['company']['address']['direccion'] ?? ''));

$company = new Company();
$company->setRuc((string) $p['company']['ruc']);
$company->setRazonSocial((string) $p['company']['razonSocial']);
$company->setNombreComercial((string) ($p['company']['nombreComercial'] ?? $p['company']['razonSocial']));
$company->setAddress($companyAddr);

$clientAddr = new Address();
$clientAddr->setUbigueo((string) ($p['client']['address']['ubigueo'] ?? ''));
$clientAddr->setCodigoPais((string) ($p['client']['address']['codigoPais'] ?? 'PE'));
$clientAddr->setDireccion((string) ($p['client']['address']['direccion'] ?? ''));

$client = new Client();
$client->setTipoDoc((string) $p['client']['tipoDoc']);
$client->setNumDoc((string) $p['client']['numDoc']);
$client->setRznSocial((string) $p['client']['rznSocial']);
$client->setAddress($clientAddr);

$details = [];
foreach ($p['details'] as $row) {
    $d = new SaleDetail();
    $d->setUnidad((string) $row['unidad']);
    $d->setCantidad((float) $row['cantidad']);
    $d->setCodProducto((string) $row['codProducto']);
    $d->setDescripcion((string) $row['descripcion']);
    $d->setMtoValorUnitario((float) $row['mtoValorUnitario']);
    $d->setMtoValorVenta((float) $row['mtoValorVenta']);
    $d->setTipAfeIgv((string) $row['tipAfeIgv']);
    $d->setMtoBaseIgv((float) $row['mtoBaseIgv']);
    $d->setPorcentajeIgv((float) $row['porcentajeIgv']);
    $d->setIgv((float) $row['igv']);
    $d->setTotalImpuestos((float) $row['totalImpuestos']);
    $d->setMtoPrecioUnitario((float) $row['mtoPrecioUnitario']);
    if (!empty($row['mtoValorGratuito'])) {
        $d->setMtoValorGratuito((float) $row['mtoValorGratuito']);
    }
    $details[] = $d;
}

$invoice = new Invoice();
$invoice->setUblVersion('2.1');
if (!empty($p['tipoOperacion'])) {
    $invoice->setTipoOperacion((string) $p['tipoOperacion']);
}
$invoice->setTipoDoc((string) $p['tipoDoc']);
$invoice->setSerie((string) $p['serie']);
$invoice->setCorrelativo((string) $p['correlativo']);
$invoice->setFechaEmision(new DateTime((string) $p['fechaEmision']));
$invoice->setTipoMoneda((string) $p['tipoMoneda']);
$invoice->setCompany($company);
$invoice->setClient($client);
$invoice->setDetails($details);
$invoice->setMtoOperGravadas((float) ($p['mtoOperGravadas'] ?? 0));
$invoice->setMtoIGV((float) ($p['mtoIGV'] ?? 0));
$invoice->setTotalImpuestos((float) ($p['totalImpuestos'] ?? 0));
$invoice->setValorVenta((float) ($p['valorVenta'] ?? 0));
$invoice->setSubTotal((float) ($p['subTotal'] ?? 0));
$invoice->setMtoImpVenta((float) ($p['mtoImpVenta'] ?? 0));
if (array_key_exists('mtoOperGratuitas', $p)) {
    $invoice->setMtoOperGratuitas((float) $p['mtoOperGratuitas']);
}
if (array_key_exists('mtoIGVGratuitas', $p)) {
    $invoice->setMtoIGVGratuitas((float) $p['mtoIGVGratuitas']);
}
if (array_key_exists('totalAnticipos', $p)) {
    $invoice->setTotalAnticipos((float) $p['totalAnticipos']);
}
if (!empty($p['anticipos']) && is_array($p['anticipos'])) {
    $anticipos = [];
    foreach ($p['anticipos'] as $row) {
        $ant = new Prepayment();
        $ant->setTipoDocRel((string) ($row['tipoDocRel'] ?? ''));
        $ant->setNroDocRel((string) ($row['nroDocRel'] ?? ''));
        $ant->setTotal((float) ($row['total'] ?? 0));
        $anticipos[] = $ant;
    }
    $invoice->setAnticipos($anticipos);
}

$builder = new InvoiceBuilder();
echo $builder->build($invoice);
